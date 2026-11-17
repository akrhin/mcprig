# mcprig — repository map

## Responsibility

One static Go binary that puts many MCP servers behind one MCP endpoint. A set is
a named group of servers with its own port and its own key; a client that would
otherwise pay for a dozen tool lists sees nine meta-tools instead
(`README.md:10-24`). The binary also serves the operator panel that manages the
registry, keys and live calls.

Two processes come out of one `main`:

- **set process** — `mcprig serve --config <set>/servers.json`: an HTTP endpoint
  with bearer auth that speaks MCP to clients and proxies calls to that set's
  downstream servers.
- **panel** — `mcprig panel`: the registry, keys, secrets and one supervised set
  process per set (`internal/manager/codemap.md`).

## Design

- Registry of downstream servers and sets, one `registry.json` with atomic
  writes (`internal/registry/codemap.md`).
- Policy split between the two sides: `internal/config` parses a set's
  `servers.json` and holds permissions/profiles/rate limits/masking; the set
  process enforces them (`internal/mcpserver/codemap.md`).
- Lazy downstream connections: nothing is spawned until a call needs it
  (`internal/downstream/codemap.md`).
- Fail-closed where a mistake is silent: unkeyed set refuses to start, unapproved
  tool descriptor is denied, sandbox failure denies, unreadable lock file denies
  (`internal/toollock/codemap.md`, `internal/sandbox/codemap.md`).
- Panel and set never share memory — reload is a file plus a nonce plus a signal
  (`internal/manager`, `runtime.go:40-41`).
- No build step for the UI: the panel is hand-written JS/CSS embedded with
  `go:embed` (`web/codemap.md`).

## Repository Directory Map

| Directory | Responsibility | Map |
| --- | --- | --- |
| `cmd/` | Process entry points: the shipped binary and a test fixture server | [View Map](cmd/codemap.md) |
| `internal/` | All library code: panel, MCP facade, HTTP surface, downstream, config, registry, OAuth, stats, sandbox, quarantine, path and log hygiene, cleanup helpers | [View Map](internal/codemap.md) |
| `web/` | Panel front end (SPA, no framework) plus `embed.go` that carries it in the binary | [View Map](web/codemap.md) |
| `e2e/` | End-to-end suites: lifecycle (panel + set + stdio downstream), contract against a live BASE, proxy, keys, stdio | — |
| `deploy/` | systemd units (`mcprig.service`, `mcprig-set@.service`, `mcprig-warm.{service,timer}`), `docker-compose.yml`, example sets | — |
| `scripts/` | Gates and stands: `contract-e2e.sh`, `sandbox-e2e.sh`, `dev-real-set.sh`, `docs-check.sh`, `release-notes.sh` | — |
| `docs/` | Documentation: `docs/en/` (canon), `docs/ru/` (mirror), `docs/history/` (plans, audits, reference contract), `docs/diagrams/` | — |
| `design/` | Logos, tokens, UI screenshots; not part of the build | — |

## Flow

Client → set endpoint (`internal/httplisten`: bearer, Host/Origin, 4 MiB cap) →
`internal/mcpserver` meta-tool (permissions → rate limit → quarantine → per-server
slot) → `internal/downstream` session → upstream server. Every call is counted in
`internal/stats`; an answer above the cap is spilled to the `tool-out` directory
and referenced by path or URL (`internal/toolout/codemap.md`).

Panel: browser → `internal/manager` HTTP API → `internal/registry`; starting a set
composes its `servers.json` (`internal/manager/setconfig.go`), resolves secrets,
spawns `mcprig serve`, and reloads it through `reload.request` + SIGHUP.

## Integration

- Build and gates: `go build ./...`, `go vet ./...`, `go test ./... -race -count=1
  -p 1` (`-p 1` is mandatory: `manager` and `e2e` both bind ports 9100–9199),
  `bash scripts/docs-check.sh`.
- External dependencies that shape the code: `github.com/modelcontextprotocol/go-sdk`
  v1.8.0 (protocol default `2026-07-28`), `golang.org/x/oauth2`,
  `go.opentelemetry.io/otel`, `golang.org/x/time/rate`.
- State on disk: config `$XDG_CONFIG_HOME/mcprig/servers.json`; data
  `$XDG_STATE_HOME/mcprig` (override `MCPRIG_DATA_DIR`) — `registry.json`,
  `sets/<id>/{servers.json,oauth.json,tools.lock.json,logs}`,
  `tool-out/` (`cmd/mcprig/main.go:564-574`, `internal/manager/api.go:98-107`).
- Diagram sources live as `.json` beside generated `.html`/`.png` in
  `docs/diagrams/`; only the `.json` is edited (`AGENTS.md`).

## Keeping this map current

State for change detection is `.slim/codemap.json`. The skill ships
`scripts/codemap.mjs`:

```bash
node …/codemap.mjs changes --root ./   # what changed since the last update
node …/codemap.mjs update  --root ./   # record the new state
```

After changing code in a directory, re-read that directory's `codemap.md` before
trusting it: line references are manual and rot on edit.
