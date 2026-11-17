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
| `stdio` | `-config` main.go:106 |
| `auth` | `-set` auth.go:73, `-config` auth.go:74, `-timeout` (6m) auth.go:75; positional server name auth.go:82 |
| `approve` | `-set` approve.go:33, `-all` approve.go:34, `-panel` approve.go:35, `-password` approve.go:36, `-secret` approve.go:37, `-user` approve.go:38; positional `server/tool` approve.go:53 |
| `panel` | `-h`/`--help` prints the environment table (`newPanelFlags` main.go:107-118, `panelEnv` main.go:125-153); any other argument is a `usageError` (main.go:170-171) |
| `init` | `-client` init.go:27, `-set` init.go:28, `-remote` init.go:29, `-key` init.go:30, `-key-file` init.go:31, `-binary` init.go:32, `-config` init.go:33 |
| `doctor` | `-config` doctor.go:71, `-json` doctor.go:72 |
| `warm` | `-url` warm.go:62, `-panel` warm.go:63, `-set` warm.go:64, `-all` warm.go:65, `-timeout` (2m) warm.go:66, `-token` warm.go:67, `-token-file` warm.go:68, `-config` warm.go:69, `-json` warm.go:70, `-password` warm.go:71, `-secret` warm.go:72, `-user` warm.go:73 |
| `status` | `-panel` status.go:72, `-set` status.go:73, `-json` status.go:74, `-timeout` (10s) status.go:75, `-password` status.go:76, `-secret` status.go:77, `-user` status.go:78 |
| `logs` | `-panel` logs.go:47, `-set` logs.go:48, `-server` logs.go:49, `-n` (200) logs.go:50, `-f` logs.go:51, `-json` logs.go:52, `-password` logs.go:53, `-secret` logs.go:54, `-user` logs.go:55 |

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

Mode dispatch (`run`, main.go:264-389) maps `stdio|auth|panel|init|doctor|warm|
approve|status|logs` to their `run*`; anything else is `serve`. `panel` is one
call, `manager.Run(manager.FromEnv(version))` (main.go:275): the panel is
`internal/manager`, configured by environment rather than flags.
Layering: `cmd/mcprig` carries no protocol logic. `serve` assembles
`internal/mcpserver` (proxy) over `internal/downstream` (child processes, OAuth)
and wraps it with `internal/httplisten` (main.go:343-346); `status`, `logs`,
`warm`, `approve` are HTTP clients of the panel API; `doctor` reads
`registry.json`, the config and probes sockets (`auth` is
`internal/downstream` with a print-only browser, auth.go:99-109).

## Flow

`serve` (main.go:291-389): parse flags → `-version` prints and exits →
`internal/config.Load` → `slog` on stderr → OTLP tracing when the env asks
(`startTracing`, main.go:599) → `oauth.OpenStore(stateDir())` →
`downstream.Connector` with an env whitelist (`downstream.EnvFor`) →
`mcpserver.New` → `mgr.LockPath = lockPath()` →
`stats.NewRecorder(MCPRIG_SET)` assigned before warm-up so the panel sees schema
weight → history at `<dir of config>/stats`, warning only on failure
(main.go:330-335) → `go mgr.Warm` in the background → `mgr.ToolOutURL` only when
a transport token exists (main.go:342) → `httplisten.New` → listen address from
`-addr`, else `transport.host`/`transport.port`, else `127.0.0.1:9100`
(main.go:348-359) → `http.Server` with `ReadHeaderTimeout` 10s and `IdleTimeout`
60s but no `ReadTimeout`, because sets stream SSE (main.go:362) →
`SIGINT`/`SIGTERM` context → `SIGHUP` loop → `startToolout` → `ListenAndServe`
in a goroutine → log line `прокси слушает` with address and usable server count
→ 5-second graceful shutdown on signal (main.go:386-388).

SIGHUP reload (main.go:392-434): read the nonce from `<config dir>/reload.request`
before loading; `config.Load`; on failure `mgr.ReloadFailed` and the previous
config keeps serving; on success apply the auth policy to the listener
(`handler.SetAuth`, main.go:424), then `mgr.Reload`; finally write
`<config dir>/reload.status` atomically (temp file, 0600, rename —
main.go:495-532) with nonce, generation, applied, restartNeeded, error.
`stdio` (main.go:546-594) is the same assembly minus listener, auth policy and
warm-up — config, tracing, OAuth store, connector, manager, lock, recorder
labelled `stdio` (main.go:576), history at `<stateDir>/stdio/stats`,
tool-out sweeper, then `mgr.Server().Run(ctx, &mcp.StdioTransport{})`.

`panel`: `cmd` loads no config; `internal/manager` reads `registry.json`, takes
the `manager.pid` lock in the data dir (internal/manager/api.go:370-385),
listens on `MCPRIG_HOST:MCPRIG_PORT` (default `127.0.0.1:9091`) and spawns each
set as `<MCPRIG_CLI> serve --config <set>/servers.json` in its own process group
with `MCPRIG_SET` and `MCPRIG_DATA_DIR=<data>/sets/<id>`
(internal/manager/runtime.go:242-243, 841-846), writing set logs to
`<data>/sets/<id>/mcprig.log` (internal/manager/setconfig.go:195-197); this
package itself writes only `reload.status`.

`auth` resolves the target (auth.go:32-59): `-set` reads
`<stateDir>/sets/<set>/servers.json` and stores the token in that same directory;
`-config` keeps the store in the caller's data dir. It prints the link, waits up
to `-timeout`, and reports whether `oauth.json` was written.
`warm` has three paths after pre-checks (warm.go:82-93): `-config` in-process via
`internal/mcpserver` (warm.go:170), `-all` from `/api/sets` (warm.go:400), else a
single set, with `-url` bypassing the panel or the port resolved by name
(warm.go:358-380). Token precedence `--token` → `--token-file` → `MCPRIG_TOKEN`
→ `~/.config/mcprig/<set>.token` (warm.go:306-326); a failed set makes the
process exit `1` via `errWarmProblems`.

## Integration

Internal packages here: `config`, `downstream`, `httplisten`, `manager`,
`mcpserver`, `oauth`, `stats`, `toolout` (main.go:22-30), plus `registry` and
`sandbox` (doctor.go:22-26) and `toollock` (approve.go:17). Panel API consumed
by the CLI: `POST /api/login` (panel.go:56), `/healthz`
(status.go:201), `/api/state` (status.go:125), `/api/calls?set=&limit=1`
(status.go:173), `/api/sets` (warm.go:389), `POST /api/sets/<set>/tools/approve`
(approve.go:103), `/api/sets/<id>/logs` and
`/api/sets/<id>/servers/<name>/logs` (logs.go:118-120). Every write carries
`Origin` computed from the panel URL, because the panel's CSRF check rejects a
session cookie without it (panel.go:119-121, 138-143).

On-disk layout: config default `os.UserConfigDir()/mcprig/servers.json`
(main.go:534-539); state dir `MCPRIG_DATA_DIR`, else `$XDG_STATE_HOME/mcprig`,
else `~/.local/state/mcprig`, else `.mcprig` (main.go:615-627); OAuth tokens at
`<state>/oauth.json` and per set `<state>/sets/<id>/oauth.json`;
approved-descriptor lock at `<MCPRIG_DATA_DIR>/tools.lock.json`, empty path when
the variable is unset so a manual `serve` has no quarantine owner
(main.go:633-638); tool-out dumps under `<MCPRIG_DATA_DIR or os.TempDir()>/tool-out`
(internal/toolout/toolout.go:47-55); warm token file
`~/.config/mcprig/<set>.token` (warm.go:320, 340-346). Environment read directly
here: `MCPRIG_PPROF` (main.go:38), `MCPRIG_SET` (main.go:323),
`MCPRIG_DATA_DIR`/`XDG_STATE_HOME` (main.go:616-628); the full set `panel`
reads is the `panelEnv` table, which `panel -h` prints (main.go:125-153) —
OpenBao's `BAO_*`/`OPENBAO_*` arrive from `internal/manager/secrets.go:115-128`.
Also
`MCPRIG_PANEL_URL`/`MCPRIG_PORT` (panel.go:32-35), `MCPRIG_PASSWORD`
(panel.go:46), `MCPRIG_PROXY_SECRET`, `MCPRIG_USER_HEADER` (panel.go:149, 159),
`MCPRIG_TOKEN` (warm.go:317), `MCPRIG_KEY` (init.go:131), plus for `doctor`
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
