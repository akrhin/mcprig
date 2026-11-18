# internal/mcpserver/

## Responsibility

The MCP server the proxy exposes to its own client: one `mcp.Server` named `mcprig` (`manager.go:560`) fronting every
downstream server of one set ("набор"). Downstream connections are lazy — nothing connects until a call needs it
(`manager.go:2`). The package owns everything between a client request and the upstream wire; how a downstream is
launched belongs to `internal/downstream`.

Duties: five catalog/call meta-tools plus four resource/prompt meta-tools, argument validation, permission and profile
gating, per-caller rate limits, descriptor quarantine, secret scrubbing, capping oversized answers into files, OTel
tracing, live config reload.

## Design

- `Manager` (`manager.go:76`) — one set. `mu sync.RWMutex` guards `cfg`, `entries`, `limits`; no network call runs under
  it (`manager.go:79`). `generation` counts reload attempts including rejected ones (`manager.go:80-83`).
  `slots *serverSlots` (`manager.go:101`) bounds parallel calls per downstream; `limits *rateLimits`
  (`manager.go:104`); `Stats *stats.Recorder` (`manager.go:108`) is optional. `LockPath` (`manager.go:112`) empty
  disables quarantine; `lockErr` (`manager.go:119`) poisons it until a catalog walk writes the file again.
  `ToolOutURL` (`manager.go:130`) switches the truncation marker to a `/tool-out/` URL for keyed HTTP clients.
- `entry` (`manager.go:133`) — `srv atomic.Pointer[config.Server]` swapped whole on reload, `conn conn`,
  `secrets []string`, cached health, `sandboxNote`, and the `tools` catalog cache (`manager.go:152-156`). `conn`
  (`manager.go:1811`) is the cached session with retired/closed marks (`session` at `manager.go:1837`).
- `rateLimits` (`ratelimit.go:108`) — token buckets keyed by `sha256(identity \0 server/tool)` truncated to 8 bytes
  (`ratelimit.go:101`); `bucketIdle = 10m` (`ratelimit.go:23`), `maxBuckets = 4096` (`ratelimit.go:30`). `newRateLimits`
  returns nil for an empty block (`ratelimit.go:115`), so a set without `rateLimit` keeps no state.
- `serverSlots` (`invokemany.go:268`) — one bounded channel per server name, `maxPerServer = 4`
  (`invokemany.go:21`); `maxFanOut = 8` per `invoke_many` (`invokemany.go:18`).
- `spillWriter` (`toolout.go:318`) — `maxBytes + utf8.UTFMax` in memory, the rest streamed to a `0600` file created on
  first overflow.
- `serverHints` (`hints.go:14`) — one line per known server ("when this one is the right one, where to start"), because
  serena, socraticode and xerj all look like "search". `serverHint` (`hints.go:34`) lets a set's own `Server.Hint` win
  and returns an empty string for an unknown server, so the field is omitted rather than misleading; `listServers`
  copies it into `serverInfo.Hint` (`manager.go:782`).

Meta-tools, registered in `Manager.Server()` and wrapped by `recordMeta` (`manager.go:688`), which counts them on the
pseudo-server `mcprig` unless the call came from the panel's service context (`stats.ServiceCall(ctx)`):

- `list_servers` (`manager.go:563`) → `listServers` (`manager.go:738`); health cached `healthconst.TTL = 15s`
  (`internal/healthconst`), probes bounded by `maxProbes = 8` (`manager.go:37`, `probeSlots` `manager.go:70`), `maxPages = 20`;
  each entry carries `hint` (`serverHint` `hints.go:34`).
- `list_commands` (`manager.go:572`) → `listCommands` (`manager.go:912`): `server`|`servers`, `prefix`, `namesOnly`,
  `refresh`, `maxBytes`; filtered by `profileCommands` (`toolout.go:410`).
- `describe_commands` (`manager.go:602`) → `describeCommands` (`manager.go:1037`), filtered by `profileTools`
  (`toolout.go:428`) so a schema the catalog hides stays hidden.
- `invoke_command` (`manager.go:623`) → `invokeCommand` (`manager.go:1123`); `command` is a synonym of `command_name`,
  downstream input goes in `parameters`, `timeoutMs` (1000..600000, `checkTimeoutMs` `manager.go:1284`) bounds this call
  and the answer names the limit actually used in `_meta.timeoutMs` (`withTimeoutMeta` `manager.go:1796`).
- `invoke_many` (`manager.go:646`) → `invokeMany` (`invokemany.go:56`): array in `calls` order, a failure keeps its slot
  with `ok:false`, one downstream `isError` does not cancel siblings; per-call `timeoutMs`, and a fan-out budget of
  `max(requestTimeout, max(calls[i].timeoutMs))` created only when `requestTimeout > 0` (`invokemany.go:82-92`) — with 0
  the batch has no shared deadline, so a call without `timeoutMs` is not cut by a neighbour's limit; `timeout_ms` per
  entry names the limit that call actually ran under, the budget sits in `_meta`.
- `list_resources` / `read_resource` / `list_prompts` / `get_prompt` (`resources.go:81-136`), served as meta-tools and
  only for a server whose config names the kind in `Expose` (`resources.go:18`).

Cap: `defaultMaxBytes = 32768`, `maxAllowedBytes = 1048576` (`toolout.go:37-43`), validated by `resolveMaxBytes`
(`manager.go:1261`). `list_servers`, single-server `list_commands` and `describe_commands` are uncut without an
explicit `maxBytes`.

Argument errors: `strictArgs` (`manager.go:1678`) rejects unknown top-level keys and turns `json.UnmarshalTypeError`
into a hint (`typeErrorText` `manager.go:1664`, `valueKind` `manager.go:1734`); `extraKeys` (`manager.go:1742`) does
the same per `calls[]` entry; `parametersOf` (`manager.go:1295`) accepts only an object or nothing.

Reload: `Reload` (`manager.go:354`) returns `ReloadResult` (`manager.go:332`) — `Generation`, `Applied`,
`RestartNeeded`, `Err`; `keepLiveTransport` (`manager.go:528`) keeps a session whose transport fields did not change;
`ReloadFailed` (`manager.go:453`) records a rejected attempt; `publishSchema` (`manager.go:215`) reports schema
bytes/tokens/tool count to `Stats`.

## Flow

Client → `httplisten` → `m.Server()` → receiving middleware `traceCalls` (`trace.go:95`) → meta handler → downstream
and back.

1. Trace: only `tools/call` (`trace.go:97`). `traceparent` from `_meta` (`trace.go:28`) becomes the parent span
   (`spanContextOf` `trace.go:46`); version must be `00`, flags must not be `ff` (`trace.go:52`). Without an exporter a
   child span id is still minted so the client trace does not break at the proxy (`trace.go:115`, `childOf`
   `trace.go:151`). `recordOutcome` (`trace.go:170`) puts only the error type into the span — upstream error text may
   repeat secrets.
2. Validation: `strictArgs` (`manager.go:1678`) → `resolveMaxBytes` (`manager.go:1261`) → `m.find(server)`
   (`manager.go:1382`) → `srv := e.server()` as the call's snapshot (`manager.go:1150-1152`); `usable`
   (`manager.go:1618`) rejects a disabled or broken server and `serverProblem` (`manager.go:1620`) names it.
3. Quota before any session exists: `throttle` (`manager.go:1161`, impl `manager.go:1367`) → `rateLimits.allow`
   (`ratelimit.go:133`). Identity is the key fingerprint, else peer address, else `"stdin"` (`identityOf`
   `manager.go:1354`); a refused call cancels its reservation so it costs no token (`ratelimit.go:146-152`) and
   records `Stats.RecordThrottled` (`manager.go:1377`).
4. Policy: `gate` (`permissions.go:51`) answers from **one** config snapshot — `config.DenyReason` for the lazy-mcp
   `permissions` block (`permissions.go:17`), then `callerProfile` (`permissions.go:31`), which finds the key by
   fingerprint in `cfg.Transport.KeyProfiles`, never by display label. A profile only narrows; a missing profile block
   denies everything (`permissions.go:60`). Denial returns `blockedResult` (`permissions.go:67`): `IsError` plus
   `Meta["lazyMcpPolicy"]="blocked"`.
5. Catalog/quarantine: `toolForCall` (`quarantine.go:113`) re-reads `tools/list` when the cache is empty and quarantine
   is on, then rejects an unknown name — a cold cache must not open a bypass (`quarantine.go:114-116`). `quarantine`
   (`quarantine.go:78`) denies when the descriptor hash differs from the approved one, and also when `tools.lock.json`
   is unreadable or was not written — unknown state is a refusal (`quarantine.go:88-94`). First appearance is
   auto-approved by `observe` (`quarantine.go:26`); a changed hash is stored in `Seen` and not approved
   (`quarantine.go:55`).
6. Slot: `m.slots.get(srv.Name)` (`manager.go:1194`); waiting honours the call context. `callCtxFor`
   (`manager.go:1449`) uses the caller's `timeoutMs` when set and `callCtx`'s `cfg.RequestTimeout` (`manager.go:1440`)
   otherwise; `callTimeoutMs` (`manager.go:1458`) reports which limit applied — the call's own wins over the set's, `0`
   means no limit — and `withTimeoutMeta` (`manager.go:1796`) puts it into `_meta.timeoutMs` after truncation. A
   sandboxed server gets its own shorter `srv.CallTimeout()` inside `callTool` (`manager.go:1507`, limit at
   `manager.go:1517`), whose expiry drops the session.
7. Downstream: `callWithRetry` (`manager.go:1223`) builds `mcp.CallToolParams{Name, Arguments, Meta: traceMeta(ctx)}` —
   only `traceparent` crosses down (`trace.go:85`) — then `callTool` (`manager.go:1507`) → `e.conn.session` → `dialFor`
   (`manager.go:1418`) → `downstream.Connector.Connect`.
8. Retry: `retryableCall` (`manager.go:1545`) qualifies only a drop before the first response byte
   (`ErrConnectionClosed`, clean EOF); an `io.ErrUnexpectedEOF`, any result including `isError`, a context error and a
   net timeout are not repeated. The repeat drops the session and waits `invokeRetryDelay = 250ms` (`manager.go:43`,
   `manager.go:1236`).
9. Back: `scrubSecrets` (`redact.go:19`) masks `srv.Secrets()` in text content only — structured content is left
   intact — **before** the counter (`manager.go:1249`), so a key repeated in an upstream error reaches neither stats nor
   the tool-out dump; counting uses the real server name (`manager.go:1255`).
10. Cap: `truncateResult` (`toolout.go:60`) returns a fitting answer untouched; otherwise the whole answer goes to the
    tool-out directory with an owner sidecar and the caller gets the head plus a marker naming the path or the
    `/tool-out/` URL (`toolout.go:262`). `invoke_many` uses `truncateValue` (`toolout.go:267`) so a plain text answer
    stays a string. Both the dump and its `.owner` sidecar are opened with `O_CREATE|O_EXCL` (`toolout.go:384-411`):
    `os.WriteFile` would follow a symlink planted in the dump directory and truncate someone else's file.

## Integration

Consumers: `cmd/mcprig/main.go:305` (HTTP `serve`), `:555` (`stdio`), `:590` (`SetupTracing`), `:356-359`/`:418` (SIGHUP
reload loop and `ReloadResult` written to `reload.status`), `cmd/mcprig/warm.go:181` (`warm`). `internal/manager`
imports it in a test only.

Internal packages: `internal/config` (`Config`, `Server`, `Permissions`, `Profile`, `RateLimit`, `config.DenyReason`,
`srv.Secrets()`, `Server.Expose`, `Server.CallTimeout()`); `internal/downstream` (`Connector`, `EnvFor`, and the
`OnSandboxApplied`/`OnSandboxFallback`/`OnToolsChanged` hooks installed in `New`, `manager.go:277-296`);
`internal/stats` (`Recorder`, `ClientKey`, `ClientAddr`, `ClientFrom`, `ServiceCall`, `Schema`, `TokensOf`,
`RecordThrottled`); `internal/toollock` (`Update`, `Read`, `ReadFile`, `Hash`, `Key`, `Message`); `internal/toolout`
(`Dir`, `Base`, `AnonymousOwner`, `OwnerPath`).

External: `github.com/modelcontextprotocol/go-sdk/mcp` v1.8.0, `go.opentelemetry.io/otel` with `sdk/trace` and
`otlp/otlptrace/otlptracehttp`, `golang.org/x/time/rate`.

Coupling to watch: `healthconst.TTL`/`healthconst.Timeout` (`internal/healthconst`) — единственный источник для
прокси и панели (`internal/manager/health.go` алиасит их), поэтому расходиться в том, кто здоров, им не из чего.
