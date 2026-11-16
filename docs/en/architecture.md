[← Docs index](index.md)
EN · [RU](../ru/architecture.md)

This file explains how the processes fit together, why each set is its own
process, and where health, statistics and OAuth live.

# Architecture

## Layers

```text
MCP client ──HTTP/stdio──▶ set process (mcprig serve) ──stdio/HTTP──▶ downstream MCP servers
                                  ▲
                                  │ spawn, config, /stats
                            mcprig panel
```

| layer | responsibility |
| --- | --- |
| panel | registry, key issuing, supervision, secrets, HTTP API and UI |
| set process | the five meta-tools, downstream sessions, health, counters, OAuth |
| downstream | a stdio child process (optionally wrapped in `bwrap` by `internal/sandbox`), or an HTTP MCP endpoint |

The panel is a control plane: it never proxies MCP traffic. Clients talk to
the set process directly, at `http://<host>:<port>/mcp` with the set's bearer
token (or over stdio when the client spawns `mcprig stdio`).

## One process per set

`mcprig panel` starts a set by spawning
`<cli> serve --config <dataDir>/sets/<id>/servers.json` with its own process
group, `MCPRIG_DATA_DIR` and `MCPRIG_SET=<id>`. Consequences:

- a crash or restart of one set does not affect another;
- each set has its own port (9100–9199), bearer key and log file;
- a set can be run alone under systemd, without the panel;
- a downstream stdio process is a grandchild of the panel and lives in its own
  process group: the set closes its sessions on shutdown, and every session
  signals its group — `SIGTERM`, a short wait, then `SIGKILL` — so a server that
  wraps its work (`sh -c "npx … & wait"`) takes its children with it.

The panel materializes `servers.json` atomically (`0600`) before every start,
so the file on disk is always what the running set was given. Start checks
that the port is free, spawns the process, then waits up to 15 s for the port
to accept a TCP connection.

Restart policy:

| event | behaviour |
| --- | --- |
| exit code 0 | treated as a normal exit, no restart |
| exit code ≠ 0 | restart after `1s << n`, capped at 30 s, at most 5 times |
| port never opens | process killed, `набор … не поднялся: порт … не слушает` |
| stop | `SIGTERM` to the process group, then `SIGKILL` after 5 s |

Logs are appended to `sets/<id>/mcprig.log`, rotate to `.log.1` at 5 MiB, and
the last 2000 lines are kept in memory for `GET /api/sets/<id>/logs`.

## Reload without a restart

`SIGHUP` to a set's `serve` process makes it reread `servers.json` and answer in
`reload.status` beside the file:
`{"nonce":"…","generation":N,"at":"RFC3339","applied":[…],"restartNeeded":[…],"error":"…"}`,
written atomically (`0600`), the generation growing on every attempt — that file
is the only acknowledgement, there is no HTTP route on the process. The signal
goes to the `serve` process alone, not to its process group: stdio children have
no `SIGHUP` handler and must survive an edit of their neighbour. The supervisor
puts a random `nonce` into `reload.request` before the signal and accepts only an
ack carrying it, so a late answer to an earlier attempt is never taken for this
one; a restart removes both files, so the new process starts counting from 1.

What one reload applies:

| change | what happens |
| --- | --- |
| server added | record added, nothing connects until its first call |
| server removed | session closed, tool cache dropped |
| `command`, `args`, `cwd`, `url`, `headers`, `env`, `envAllow`, `oauth`, `sandbox` | that server's session is closed and reopens with the new fields on the next call; a call in flight to it fails instead of being drained |
| `permissions`, `enabled`, `description`, `tags` | updated in place, session and tool cache kept |
| untouched server | same record, same session, same cache |
| set `rateLimit`, `profiles` | applied in place; `requestTimeout` and `healthMonitor` are constants the manager writes and never change |
| `authToken`, `authTokens` | applied live: the panel swaps the listener's token set on reload, so a key issued or revoked arrives without a restart. A changed single `authToken` — the set's primary key — still lands in `restartNeeded` for older processes, and the panel then reports `reloadError` instead of claiming the change applied |
| `host`, `port`, `path`, `type`, `allowAnonymous`, `allowedHosts`, `clientNames` | listed in `restartNeeded`, the old values stay in force |

`keyProfiles` is applied live: the manager reads it per request, unlike the
listener fields above. A file that does not load at all leaves the process on
the previous config with the cause in `error` and nothing in `applied`. Every
attempt writes one log line: `reload: применено N, изменено M, удалено K,
generation G`.

## Lazy connections

Nothing is connected at startup. The first tool call that needs a server opens
its session; later calls reuse it. A concurrent first call shares the in-flight
connect (single-flight per server), and a failed attempt is retried on the next
call, not cached forever.

If the downstream closes the session (`ErrConnectionClosed`), `invoke_command`
resets the session, reconnects once and repeats the call. A second failure is
returned with its reason.

## Health

`list_servers` probes servers by opening a session, with:

| parameter | value |
| --- | --- |
| cache TTL | 15 s, failures included |
| timeout per probe | 15 s |
| concurrency | at most 8 probes at once |

A disabled server is not probed and reports `healthy: true`: disabled is not
the same as broken. A server with `_configError` reports `healthy: false` and
the error text without any connection attempt.

The panel's own health view (`GET /api/sets/<id>/servers`) polls the set's
`list_servers` and caches the answer for 15 s with a single-flight guard, so
opening the dashboard does not multiply probes across browser tabs.

## Statistics

Counters live in the set process (`internal/stats`): per client, server and
tool, with call/failure counts, total milliseconds, p50/p95 from a 256-sample
reservoir, and a ring of the last 512 calls. In stdio mode the client label is
`stdio` — the transport is the only client the process has — and in HTTP mode it
comes from the `User-Agent` product token plus a 6-character fingerprint of the
presented key. Labels and tool
names are capped at 64 bytes; the number of counters is capped at 4096, the
rest collapses into `(прочее)`; error text in the ring is capped at 512 bytes.

The panel does not keep counters: `GET /api/calls` polls `GET /stats` of every
running set with a 2 s timeout and merges the answers; a stopped set is read
from its raw journal on disk over the last 7 days instead. A running set that
does not answer is reported only by id. Tokens are not counted, because MCP
responses carry no standard usage field.

## HTTP transport

Each set serves one stateless endpoint:

- `2026-07-28` per request, with the same handler answering older protocol
  versions;
- `GET`, `POST`, `DELETE` on `transport.path` (default `/mcp`);
- `Host` and `Origin` checked against `allowedHosts`;
- bearer token compared with every active key's sha256 in constant time per
  element; a set serves its whole list of active keys, and the caller's
  fingerprint — hence its profile and limits — comes from the token that
  matched;
- 4 MiB request body cap;
- no read timeout (an SSE stream must not be cut), 60 s idle timeout;
- `GET /healthz` public, `GET /stats` protected by the same bearer.

## OAuth

The client-side flow runs inside the set process and only when a downstream
answers 401 or 403. It is an authorization-code flow with PKCE; dynamic client
registration, code exchange and refresh are handled by the MCP SDK
(`golang.org/x/oauth2` underneath).

| item | value |
| --- | --- |
| redirect listener | `127.0.0.1:<callbackPort>`, default 8947 |
| redirect URI | `http://127.0.0.1:<port>/callback`, overridable |
| default port | fixed on purpose: some authorization servers only accept a hand-registered URI |
| token storage | `<data dir>/oauth.json` (`0600`), client registrations in `oauth-clients.json` |
| token key | first 32 hex of `sha256(serverName + "\x00" + serverURL)` |

The panel runs the same flow for a person at a browser: **authorize** in the
server row, the link in a dialog, and the code arrives at `GET
/oauth/callback` of the panel's own address. The panel then restarts the set,
because a running process reads its token at start.

The listener binds nothing until a token is actually needed. Tokens survive a
restart, so a refresh does not require the browser again.

## Tracing

A client may send a W3C `traceparent` in the `_meta` of its request (SEP-414).
It is accepted only if it validates: version `00`, 32 hex trace-id, 16 hex
parent-id, 2 hex flags with no `ff`, non-zero ids. A malformed one is dropped
silently: the call runs without a trace, because a broken header is not a
client error.

One `tools/call` makes one span, `mcp.tool.call`, carrying `mcp.tool.name`,
`mcp.server.name`, `mcp.command.name`, `mcp.tool.outcome` (`ok`, `error`,
`blocked` by policy) and `mcp.trace.source` — `client` when the caller's trace
was continued, `proxy` when it starts here. A downstream connection is
`mcp.server.connect`, a config reload `mcp.reload`. Spans hold names only: a
key, its fingerprint and its token never reach a span, so a client-sent trace
is not audit evidence.

The client's `traceparent` becomes the parent of that span, and the span's own
id goes down to the upstream in the `_meta` of every `invoke_command`,
`read_resource` and `get_prompt`: the caller's trace continues through the
proxy into the server. Nothing else from the client's `_meta` is forwarded — it
belongs to the caller, not to the upstream.

Spans are exported over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set.
Without it no provider is installed: nothing is recorded, no connection is
made, and only the `traceparent` forwarding stays, on ids minted for it. The
environment is in [configuration.md](configuration.md#tracing).

## See also

- The same picture as a diagram: interactive in
  [diagrams/mcprig.architecture.en.html](../diagrams/mcprig.architecture.en.html),
  raster in
  [diagrams/mcprig.architecture.en.png](../diagrams/mcprig.architecture.en.png).
- [configuration.md](configuration.md) — the per-set config this process reads.
- [meta-tools.md](meta-tools.md) — the surface a client actually talks to.
- [panel.md](panel.md) — the manager's HTTP API and UI.
- [dev/development.md](dev/development.md) — the gates that keep this design honest.
