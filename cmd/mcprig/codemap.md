# cmd/mcprig/

## Responsibility

`package main` of the single `mcprig` binary: process entry and the CLI surface;
every subcommand delegates to an `internal/` package or talks HTTP to a panel.
The nine non-test files:

- `main.go` — entry, dispatch, `serve`, `stdio`, SIGHUP reload, state/lock paths.
- `panel.go` — shared panel HTTP client (login, proxy headers, CSRF `Origin`).
- `status.go` — one-shot panel snapshot, human table and `-json`.
- `logs.go` — tail/follow of a set's or server's log via the panel.
- `warm.go` — pre-fill a set's tool cache over HTTP or in-process.
- `approve.go` — approve a changed tool descriptor through the panel API.
- `auth.go` — interactive OAuth login outside any client request.
- `init.go` — print a client config fragment; writes nothing.
- `doctor.go` — read-only health report over files, registry, ports, `bwrap`.

Top-level subcommands (dispatch: `main.go:179-247`):

| command | flags (definition site) |
| --- | --- |
| `serve` (default) | `-config` main.go:98, `-version` main.go:99, `-addr` main.go:100 |
| `stdio` | `-config` main.go:157 |
| `auth` | `-set` auth.go:73, `-config` auth.go:74, `-timeout` (6m) auth.go:75; positional server name auth.go:82 |
| `approve` | `-set` approve.go:31, `-all` approve.go:32, `-panel`/`-password`/`-secret`/`-user` from `panelFlags` (panel.go:34-39); positional `server/tool` approve.go:47 |
| `panel` | `-h`/`--help` prints the environment table (`newPanelFlags` main.go:107-118, `panelEnv` main.go:125-153); any other argument is a `usageError` (main.go:170-171) |
| `init` | `-client` init.go:27, `-set` init.go:28, `-remote` init.go:29, `-key` init.go:30, `-key-file` init.go:31, `-binary` init.go:32, `-config` init.go:33 |
| `doctor` | `-config` doctor.go:70, `-json` doctor.go:71 |
| `warm` | `-url` warm.go:61, `-set` warm.go:62, `-all` warm.go:63, `-timeout` (2m) warm.go:64, `-token` warm.go:65, `-token-file` warm.go:66, `-config` warm.go:67, `-json` warm.go:68, panel flags warm.go:69 |
| `status` | `-set` status.go:70, `-json` status.go:71, `-timeout` (10s) status.go:72, panel flags status.go:73 |
| `logs` | `-set` logs.go:47, `-server` logs.go:48, `-n` (200) logs.go:49, `-f` logs.go:50, `-json` logs.go:51, panel flags logs.go:52 |

## Design

Argument parsing is stdlib `flag` only: one `flag.FlagSet` per subcommand with
`flag.ContinueOnError`, built by a `new*Flags` constructor returning the set and a
config struct (main.go:91-102). No third-party CLI library.
`parseArgs` (main.go:179-247) picks the mode, then the command re-parses the same
flags, so `-h` and bad values behave the same in dispatch and in `run*` (`serve`
is parsed twice: main.go:195 and main.go:291). Empty argv or an argv starting
with `-` is `serve` (main.go:184-190), and that form prints the program name in
help instead of `serve`. An unknown subcommand is a `usageError`, never a silent
`serve` (main.go:246). `parseFlags` rejects leftover positionals
(main.go:163-174), which is why `auth` and `approve` parse inside their own
`run*` — they take positional arguments (main.go:205-210). Exit codes
(`exitCode`, main.go:74-83): `0` for `flag.ErrHelp`, `2` for `usageError`, `1`
otherwise; `doctor` and `warm` problems exit `1` without an extra line because
their report is already printed (main.go:64-67).

Mode dispatch (`run`, main.go:264-392) maps `stdio|auth|panel|init|doctor|warm|
approve|status|logs` to their `run*`; anything else is `serve`. `panel` is one
call, `manager.Run(manager.FromEnv(version))` (main.go:275): the panel is
`internal/manager`, configured by environment rather than flags.
Layering: `cmd/mcprig` carries no protocol logic. `serve` assembles
`internal/mcpserver` (proxy) over `internal/downstream` (child processes, OAuth)
and wraps it with `internal/httplisten` (main.go:346-349); `status`, `logs`,
`warm`, `approve` are HTTP clients of the panel API; `doctor` reads
`registry.json`, the config and probes sockets (`auth` is
`internal/downstream` with a print-only browser, auth.go:99-109).

## Flow

`serve` (main.go:291-392): parse flags → `-version` prints and exits →
`internal/config.Load` → `slog` on stderr → OTLP tracing when the env asks
(`startTracing`, main.go:609) → `oauth.OpenStore(stateDir())` →
`downstream.Connector` with an env whitelist (`downstream.EnvFor`) and, in
`serve`, the on-disk server tails (`conn.ServerLogs`, main.go:317) →
`mcpserver.New` → `mgr.LockPath = lockPath()` →
`stats.NewRecorder(MCPRIG_SET)` assigned before warm-up so the panel sees schema
weight → history at `<dir of config>/stats`, warning only on failure
(main.go:331-338) → `go mgr.Warm` in the background → `mgr.ToolOutURL` only when
a transport token exists (main.go:345) → `httplisten.New` → listen address from
`-addr`, else `transport.host`/`transport.port`, else `127.0.0.1:9100`
(main.go:351-362) → `http.Server` with `ReadHeaderTimeout` 10s and `IdleTimeout`
60s but no `ReadTimeout`, because sets stream SSE (main.go:365) →
`SIGINT`/`SIGTERM` context → `SIGHUP` loop → `startToolout` → `ListenAndServe`
in a goroutine → log line `прокси слушает` with address and usable server count
(main.go:381) → 5-second graceful shutdown on signal (main.go:386-391).

SIGHUP reload (main.go:394-442): read the nonce from `<config dir>/reload.request`
before loading; `config.Load`; on failure `mgr.ReloadFailed` and the previous
config keeps serving; on success `mgr.Reload` runs first (main.go:428) — profiles
and tokens are one generation, so a key handed to the listener while the manager
still holds the rolled-back profile table would pass unprofiled — and the auth
policy reaches the listener (`handler.SetAuth`, main.go:435) only when
`RestartNeeded` is empty; otherwise one warning line names the fields that wait
for a set restart (main.go:431-433). Then write
`<config dir>/reload.status` atomically (temp file, 0600, rename —
main.go:505-542) with nonce, generation, applied, restartNeeded, error.
`stdio` (main.go:556-603) is the same assembly minus listener, auth policy and
warm-up — config, tracing, OAuth store, connector (no `ServerLogs`), manager,
lock, recorder labelled `stdio` (main.go:586-587), history at `<stateDir>/stdio/stats`,
tool-out sweeper, then `mgr.Server().Run(ctx, &mcp.StdioTransport{})`.

`panel`: `cmd` loads no config; `internal/manager` reads `registry.json`, takes
the `manager.pid` lock in the data dir (internal/manager/api.go:374-389),
listens on `MCPRIG_HOST:MCPRIG_PORT` (default `127.0.0.1:9091`) and spawns each
set as `<MCPRIG_CLI> serve --config <set>/servers.json` in its own process group
with `MCPRIG_SET` and `MCPRIG_DATA_DIR=<data>/sets/<id>`
(internal/manager/runtime.go:262-265, 905), writing set logs to
`<data>/sets/<id>/mcprig.log` and each server's stderr to
`<data>/sets/<id>/serverlogs/<name>.log` (internal/manager/setconfig.go:201-209);
this package itself writes only `reload.status`.

`auth` resolves the target (auth.go:32-59): `-set` reads
`<stateDir>/sets/<set>/servers.json` and stores the token in that same directory;
`-config` keeps the store in the caller's data dir. It prints the link, waits up
to `-timeout`, and reports whether `oauth.json` was written.
`warm` has three paths after pre-checks (warm.go:78-89): `-config` in-process via
`internal/mcpserver` (`warmLocal`, warm.go:166), `-all` from `/api/sets`
(`warmAll`, warm.go:384), else a single set (`warmOne`, warm.go:125), with `-url`
bypassing the panel or the port resolved by name (`endpointFor`, warm.go:342).
Token precedence `--token` → `--token-file` → `MCPRIG_TOKEN`
→ `~/.config/mcprig/<set>.token` (`warmToken`, warm.go:290-309); a failed set
makes the process exit `1` via `errWarmProblems` (warm.go:31).

## Integration

Internal packages here: `config`, `downstream`, `httplisten`, `manager`,
`mcpserver`, `oauth`, `stats`, `toolout` (main.go:22-30), plus `registry` and
`sandbox` (doctor.go:22-26) and `toollock` (approve.go:17). Panel API consumed
by the CLI: `POST /api/login` (panel.go:80), `/healthz`
(status.go:197), `/api/state` (status.go:120), `/api/calls?set=&limit=1`
(status.go:168), `/api/sets` (warm.go:384), `POST /api/sets/<set>/tools/approve`
(approve.go:98), `/api/sets/<id>/logs` and
`/api/sets/<id>/servers/<name>/logs` (logs.go:115-117). `fetchLogs`
(`logs.go:114-140`) turns the panel's `error` field into a CLI error instead of
an empty tail, and `printLogs` prints a server's line through `safelog.Text`
(`logs.go:191-205`). Every write carries
`Origin` computed from the panel URL, because the panel's CSRF check rejects a
session cookie without it (panel.go:120, 145).

On-disk layout: config default `os.UserConfigDir()/mcprig/servers.json`
(main.go:544-549); state dir `MCPRIG_DATA_DIR`, else `$XDG_STATE_HOME/mcprig`,
else `~/.local/state/mcprig`, else `.mcprig` (main.go:625-637); OAuth tokens at
`<state>/oauth.json` and per set `<state>/sets/<id>/oauth.json`;
approved-descriptor lock at `<MCPRIG_DATA_DIR>/tools.lock.json`, empty path when
the variable is unset so a manual `serve` has no quarantine owner
(main.go:643-648); tool-out dumps under `<MCPRIG_DATA_DIR or os.TempDir()>/tool-out`
(internal/toolout/toolout.go:47-55); warm token file
`~/.config/mcprig/<set>.token` (warm.go:304, 309). Environment read directly
here: `MCPRIG_PPROF` (main.go:38), `MCPRIG_SET` (main.go:326, 579),
`MCPRIG_DATA_DIR`/`XDG_STATE_HOME` (main.go:625-637); the full set `panel`
reads is the `panelEnv` table, which `panel -h` prints (main.go:125-153) —
OpenBao's `BAO_*`/`OPENBAO_*` arrive from `internal/manager/secrets.go:115-128`.
Also
`MCPRIG_PANEL_URL`/`MCPRIG_PORT` (panel.go:42-50), `MCPRIG_PASSWORD`
(panel.go:70), `MCPRIG_PROXY_SECRET`, `MCPRIG_USER_HEADER` (panel.go:173, 183),
`MCPRIG_TOKEN` (warm.go:301), `MCPRIG_KEY` (init.go:131), plus for `doctor`
`MCPRIG_BIND_IP`/`MCPRIG_HOST`/`MCPRIG_PORT`/`MCPRIG_PUBLIC_URL`/`MCPRIG_AUTH`/
`MCPRIG_PASSWORD_HASH` (doctor.go:88-98).
`deploy/` relation: `mcprig.service` runs `mcprig panel` with
`EnvironmentFile=~/.config/mcprig/panel.env`, `MCPRIG_DATA_DIR=~/.local/state/mcprig`,
`MCPRIG_HOST=127.0.0.1`, `MCPRIG_PORT=9091` (deploy/mcprig.service:8-12);
`mcprig-set@.service` runs `serve --config ~/.config/mcprig/sets/%i/servers.json`
with `MCPRIG_DATA_DIR=~/.local/state/mcprig/sets/%i`, the per-set state dir that
`lockPath` and the OAuth store key off (deploy/mcprig-set@.service:9-11);
`mcprig-warm.service` (installed as `mcprig-warm@.service`) runs `warm --set %i`
every ten minutes from `mcprig-warm.timer`, with
`MCPRIG_PANEL_URL=http://127.0.0.1:9091` (deploy/mcprig-warm.service:16-20).
The hidden guard mode is entered by `downstream.RunChildGuardMode(os.Args)`
before any flag parsing (main.go:57) and is reachable by no subcommand.
