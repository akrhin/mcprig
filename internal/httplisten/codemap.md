# internal/httplisten/

## Responsibility

Builds the whole HTTP surface of one set as an `http.Handler`: bearer auth,
Host/Origin policy, request-body cap, the stateless Streamable HTTP MCP
endpoint, `GET /healthz`, `GET /stats`, `GET /tool-out/{name}` and the access
log (`httplisten.go:1-4`).

It **does not listen**. There is no `net.Listen`, no unix socket, no systemd
socket activation and no TLS in this package: `serve` builds the listener itself
with `http.Server{Addr: listen}` + `ListenAndServe` (`cmd/mcprig/main.go:297-326`),
and the only TLS in the project is the panel's optional certificate
(`internal/manager/api.go:152-161`). So the directory exists to keep the policy
(identity, host, body, routes) separate from process startup and reload, not to
wrap a socket.

## Design

**Server and live policy.** `Server` embeds `http.Handler` plus `auth *authSet`
(`httplisten.go:35-38`). `SetAuth` swaps accepted tokens, openness and key names
in one step, so `POST /api/sets/<id>/keys` and a SIGHUP reload take effect
without restarting the set (`httplisten.go:40-68`): issuing a key over
`POST /api/sets/<id>/keys` delivers the new list with a reload
(`internal/manager/api.go:1753-1774`), and a SIGHUP applies it at
`cmd/mcprig/main.go:373`. Tokens are stored as `sha256` digests and compared
with `subtle.ConstantTimeCompare` against every entry, so token length does not
leak and a mismatch reveals nothing (`httplisten.go:70-105`). An empty token set
accepts nothing.

**Construction (`New`, `httplisten.go:106-204`).**

- missing `transport` or server → startup error; tokens come from
  `transport.authTokens`, falling back to a single `authToken`
  (`internal/config/config.go:745-774`);
- no token and `allowAnonymous` false → startup error, an unintended open set
  cannot start (`httplisten.go:122-125`);
- default path `/mcp`, must start with `/` (`httplisten.go:141-147`);
- empty `allowedHosts` warns and means loopback only; `"*"` warns that DNS
  rebinding protection is off for any Host (`httplisten.go:129-140`);
- tool-out lifetime from `toolout.FromEnv`, overridden by `transport.toolOutTtl`
  (`httplisten.go:149-165`);
- MCP handler: `mcp.NewStreamableHTTPHandler`, `Stateless: true`
  (2026-07-28/SEP-2567, one temporary session per POST while older protocol
  versions are synthesized by the SDK), `MaxRequestBodyBytes = 4 MiB`,
  `DisableLocalhostProtection: true` because `AllowedHosts` is the one Host
  policy (`httplisten.go:167-180,29-30`).

**Routes** (`httplisten.go:182-203`):

| route | guard | notes |
| --- | --- | --- |
| `GET /healthz` | none | plain `ok` |
| `transport.path` | `openRoute=true` | anonymous only for an open set |
| `GET /tool-out/{name}` | `openRoute=false` | never anonymous, even on an open set |
| `GET /stats` | `openRoute=true` | registered only when a `*stats.Recorder` is passed; the full snapshot goes only to the panel's service call (`X-MCPRig-Caller: panel`), every other caller — any valid key or an open set — gets `SnapshotRedacted` (`httplisten.go:187-202`) |

**guard** (`httplisten.go:273-339`) is the per-request policy: methods
`GET/HEAD/POST/DELETE` else 405 with `Allow`; `HostAllowed(r.Host)`; `Origin`,
when present, must parse and pass `HostAllowed(u.Host)`; an absent
`Authorization` passes only for `openRoute && anonymous`, a presented one is
always verified (so a client that always sends a header never gets into an open
set for free); then context enrichment — key fingerprint
(`stats.WithClientKey`), panel service call, caller host without port,
client label — and `http.MaxBytesReader` at 4 MiB.

**Host policy** (`httplisten.go:391-416`). Port is dropped on both sides; an
empty list means `127.0.0.1, localhost, ::1`; `"*"` admits everything. The same
`HostAllowed`/`HostOnly` serve the panel, so "not configured" never degrades into
"any host" (`internal/manager/auth.go:294,313,395`).

**Client label** (`httplisten.go:341-367`): owner's key name from
`transport.clientNames` wins; otherwise `stats.Label(fingerprint, userAgent)`
where the fingerprint comes only from a genuinely presented bearer value.

**tool-out** (`httplisten.go:206-271`): name must be `^[0-9a-f]{16}$` and a
`<name>.owner` file must hold the reader's fingerprint — mismatch, missing owner
or missing file is 404 (never 403: a wrong name must not confirm a dump exists);
older than the TTL is 410; the body is served by `http.ServeContent`, so `HEAD`
and `Range` work; headers `nosniff`, `no-store`, `no-referrer`.

**Access log** (`httplisten.go:418-460`): `statusWriter` records the status and
keeps `Unwrap` so SSE flushing via `http.ResponseController` still works;
`logRequests` logs method, path, status, duration, remote — never headers, body
or the `Authorization` value.

**Timeouts are not here.** They belong to the caller: `ReadHeaderTimeout: 10s`,
`IdleTimeout: 60s`, and deliberately no `ReadTimeout`, because an SSE stream must
not be cut (`cmd/mcprig/main.go:310-311`).

## Flow

Inward, one HTTP request:

1. `logRequests` wraps the mux (`httplisten.go:203`), timing the request.
2. the mux picks the route and hands it to `guard` with the route's
   openness (`httplisten.go:182-201`).
3. `guard` checks method, Host, Origin, bearer, then writes the identity into
   the request context and caps the body.
4. `next.ServeHTTP` — the SDK's stateless handler resolves tools on the one
   `*mcp.Server` the set already built, or `toolOutHandler` serves a dump.

Outward, identity for the rest of the process: `stats.Recorder` receives the
client label and key fingerprint from the context; `toolout` owns the dump
directory under `toolout.Base()`/`tool-out`.

## Integration

- `cmd/mcprig/main.go:292` builds the handler after the recorder and history,
  passes it to `http.Server`, and on SIGHUP applies a new key list through
  `handler.SetAuth` inside `reloadOnce` (`main.go:341-373`).
- `internal/manager/auth.go:292-395` reuses `HostAllowed`/`HostOnly` for the
  panel: one Host/rebinding policy, one loopback default.
- Depends on `internal/config` (`HTTPTransport`), `internal/stats` (labels,
  fingerprint, snapshots), `internal/toolout` (dump dir, owner, TTL) and
  `github.com/modelcontextprotocol/go-sdk/mcp`.
- Files on disk: `<toolout.Base()>/tool-out/<16 hex>.txt` plus `<name>.owner`.
- Tests: `httplisten_test.go`, `clientkey_test.go`, `toolout_test.go`.
