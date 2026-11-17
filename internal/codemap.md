# internal/

## Responsibility

All library code of the module. Two runtime roles share it: the **set process**
(`mcprig serve --config …`) exposes one set's MCP endpoint, and the **panel
process** (`mcprig panel`) owns the registry and supervises one set process per
set. `internal/manager` is the only place where the two roles meet.

## Design

| Package | Responsibility | Map |
| --- | --- | --- |
| `internal/manager/` | Panel: HTTP API/UI, registry, secrets chain, one supervised child per set, reload/restart decisions | [View Map](manager/codemap.md) |
| `internal/mcpserver/` | The MCP facade of one set: nine meta-tools, permissions, quotas, quarantine, secret scrubbing, answer truncation | [View Map](mcpserver/codemap.md) |
| `internal/httplisten/` | HTTP surface of a set as an `http.Handler`: bearer auth, Host/Origin policy, 4 MiB body cap, stateless MCP endpoint, `/healthz`, `/stats`, `/tool-out/` | [View Map](httplisten/codemap.md) |
| `internal/downstream/` | MCP client sessions to downstream servers: stdio children via `child-guard`, env allowlist, sandbox argv, group teardown; remote HTTP | [View Map](downstream/codemap.md) |
| `internal/config/` | One set's `servers.json` (lazy-mcp format) plus the policy primitives: permissions, profiles, rate limits, secret masking | [View Map](config/codemap.md) |
| `internal/registry/` | Panel's durable state: servers, sets, access keys, port allocation in one `registry.json` | [View Map](registry/codemap.md) |
| `internal/oauth/` | OAuth 2.0 client side for remote servers: token store, loopback code receiver, SDK handler factory | [View Map](oauth/codemap.md) |
| `internal/stats/` | Per-client call accounting: in-memory `Recorder` + JSONL journal and day/month aggregates on disk | [View Map](stats/codemap.md) |
| `internal/sandbox/` | bubblewrap argv prefix for stdio servers: read-only root, no `/home`, no network, `prlimit` | [View Map](sandbox/codemap.md) |
| `internal/toollock/` | `tools.lock.json`: approved tool descriptors, canonical sha256, shared by set and panel | [View Map](toollock/codemap.md) |
| `internal/toolout/` | `tool-out` directory: `.owner` sidecars and retention sweep (not the cut itself) | [View Map](toolout/codemap.md) |
| `internal/atomicfile/` | Atomic file replacement: temp in the same directory, `0600`, fsync, rename | [View Map](atomicfile/codemap.md) |
| `internal/healthconst/` | Health-probe budget shared by the panel and the MCP facade (`TTL`, `Timeout` = 15 s); a separate package because `manager` imports `mcpserver` in tests | [View Map](healthconst/codemap.md) |
| `internal/safepath/` | Shape checks for values that reach a filesystem path: one segment (`Name`, the registry's name rule) and a path to read (`Path`) | [View Map](safepath/codemap.md) |
| `internal/safelog/` | `Value`: a log value with control characters removed, so a name from the request cannot forge a log line | [View Map](safelog/codemap.md) |

## Flow

Set process: socket → `httplisten.Handler` (auth, policy) → `mcpserver.Manager`
(meta-tool → permissions → rate limit → quarantine → slot) → `downstream` session
→ upstream server. Call accounting goes to `stats`, truncated answers to the
`tool-out` directory, approvals to `toollock`.

Panel process: `manager.Manager` (one `ServeHTTP`) → `registry` for state →
`config`/`oauth`/`sandbox` to compose what a set needs → spawns `mcprig serve`
per set and talks to it over its own HTTP endpoint.

## Integration

`cmd/mcprig` is the only consumer of all of it. External dependencies that matter:
`github.com/modelcontextprotocol/go-sdk` (MCP), `golang.org/x/oauth2`,
`go.opentelemetry.io/otel`, `golang.org/x/time/rate`. State on disk lives under
`$XDG_STATE_HOME/mcprig` and `$XDG_CONFIG_HOME/mcprig` (see the root map).
