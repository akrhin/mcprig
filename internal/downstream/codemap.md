# internal/downstream/

## Responsibility

Opens MCP client sessions to downstream servers — local stdio processes and
remote HTTP endpoints — and owns what must survive that process: env allowlist,
sandbox argv, child-guard supervisor, group teardown, stderr, OAuth wiring.

- `connect.go:24` `Connector` is the entry point; `Connect` (`connect.go:67`)
  returns an `*mcp.ClientSession`.
- `guard.go:23` `GuardMain` is the `mcprig child-guard --` supervisor mode.
- `serverlog.go:20` `ServerLogs` keeps the on-disk tail of each server process's
  stderr: one file per server under `serverlogs/`, opened lazily by `Writer`.

## Design

**Connector is a value, not a manager.** Policy arrives through fields, so
`serve`, the panel manager, probe, browse and `warm` each build their own
(`connect.go:24-65`): `Env` (`:33`, nil = caller's environment — the
`manage`/`serve` path), `OAuth`+`Store` (`:26-27`, nil store = flow off),
`Browser` (`:45`), `Authorize` (`:49`, panel login), `SingleAuthorization`
(`:52`), `OnSandboxFallback` (`:37`), `OnSandboxApplied` (`:41`),
`OnToolsChanged` (`:59`), `ServerLogs` (`:64`, nil = stderr only in the start
error's tail).

**Two transports, one switch.** `transport` (`connect.go:125`) serves stdio and
`config.TransportHTTP`. Remote is `mcp.StreamableClientTransport` (`:197`) with
headers injected per RoundTrip (`withHeaders:307`) and a `sameHostRedirect` guard
(`:273`): Authorization would leak to another host on a cross-host redirect, so
those are refused (`:275-281`). `OAuthHandler` is set only when non-nil (`:211`) —
a typed-nil interface would make the SDK call `TokenSource` on nil.

**Protocol is pinned for stdio only.** `legacyProtocol = "2025-11-25"`
(`connect.go:93`, via `connectOptions:99`): SDK ≤ 1.9 servers lack
`server/discover` (SEP-2575) and die on it; `initialize` is universal.

**Process shape: set → guard → server, each in its own group.** The set's direct
child is `os.Executable()` running `child-guard -- <argv>` (`guardArgv`,
`connect.go:324`); `childAttr` (`procattr_linux.go:10`) sets `Setpgid` plus
`Pdeathsig: SIGTERM`, so the kernel signals the guard even when the set is
SIGKILLed and it outlives the set to kill grandchildren (`guard.go:13-22`). A
replaced binary — `/proc/self/exe` ending in " (deleted)" — skips the guard with
a warning (`connect.go:159-169`, `fileExists:334`).

**Sandbox is an argv prefix, fail-closed.** `sandbox.Wrap` (`connect.go:133`)
returns `Result{Argv, Applied, Reason}`; a deny refusal reports the reason via
`OnSandboxFallback` then fails, so `list_servers` never claims bwrap (`:133-151`).

**Server stderr also lands in a file the panel can read.** `Writer`
(`serverlog.go:58`) returns nil for a server whose name breaks the path pattern,
creates `serverlogs/<name>.log` 0600 in a 0700 directory (`:69-88`), and writes
through an `io.MultiWriter` together with the in-memory tail (`connect.go:180-184`).
A launch marker `— запуск <time> —` is written when the file is opened and on
every reattach (`serverlog.go:96,103-120`); the file is capped at
`serverLogLimit` 256 KiB (`:42`) by a whole-file `Truncate(0)` followed by a fresh
marker (`:149-154`), a single `Write` longer than the cap keeps its tail
(`:146-148`), and a failed truncation sets `broken`, which stops writing instead
of growing without bound (`:155-161`). Write errors are logged and swallowed —
a disk fault must not break the child's `Wait` (`:165-170`).

**Teardown kills groups, not pids.** `groupTransport.Connect` (`procgroup.go:22`)
records `pgid = Command.Process.Pid` (the child leads its own group);
`groupConn.Close` (`:38`) waits up to `closeGrace` 3 s after stdin closes, then
SIGTERMs the group, waits `groupGrace` 1 s, then SIGKILLs (`:47-63`), grace
zeroed when the context already expired (`:42-46`). `groupAlive` (`:105`) counts
members via `/proc/<pid>/stat`, ignoring zombies that `kill(0)` calls alive.

**Env is an allowlist, and reserved names never leave.** `baseEnvKeep`
(`env.go:14`) is `PATH HOME LANG TZ TMPDIR NODE_EXTRA_CA_CERTS` plus the proxy
address variables; prefixes `LC_ XDG_ SSL_` (`env.go:24`) always pass; `EnvAllow`
adds named host variables (`envSelect:43`); manager-owned names are filtered both
ways (`withoutReserved`, `connect.go:296`; `reservedName` → `config.ReservedEnvName`,
`env.go:29`). Server `env` wins over the inherited base (`connect.go:191`).

**Failures carry the child's own words.** Go sends a child's stderr to /dev/null,
so a bwrap/npx startup failure surfaces as bare EOF; `stderrTail`
(`connect.go:221`) keeps the last 4096 bytes (`:226`) and `Connect` appends them
to the error, scrubbed by `config.ScrubSecrets` (`:83-84`).

## Flow

Stdio child start (`Connector.Connect` → session):
1. `transport` → `sandbox.Wrap(srv, srv.Command)` (`connect.go:133`); deny →
   `OnSandboxFallback` + error; `Reason` → warn log + `OnSandboxFallback`;
   `Applied` → `OnSandboxApplied` (`:142-154`).
2. `os.Executable()` (`:162`) → `guardArgv` prepends `<exe> child-guard --`
   (`:324`); missing exe → server without guard.
3. `exec.CommandContext(context.WithoutCancel(ctx), …)` (`:172`): context values
   kept, cancellation not — the server outlives the request; plus
   `cmd.Dir = srv.Cwd`, `cmd.SysProcAttr = childAttr()`, and
   `cmd.Stderr = io.MultiWriter(tail, ServerLogs.Writer(srv))` (`:180-184`).
4. `cmd.Env = withoutReserved(base) + flatten(srv.Env)`, base being `c.Env(srv)`
   or `os.Environ()` (`:187-191`).
5. `groupTransport{CommandTransport{Command: cmd}}` returned (`:192`); the SDK
   starts the process inside `groupConn.Connect` and yields a session.
6. Close: stdin closes → up to 3 s grace → SIGTERM group → 1 s → SIGKILL group
   (`procgroup.go:38-63`).

Guard mode (`RunChildGuardMode`, `guard.go:83`, from `cmd/mcprig/main.go:57`):
1. SIGTERM handler installed first (`guard.go:26-28`), `--` stripped (`:31`).
2. `Getpgid(0)`; `Getppid() == 1` → an already-orphaned guard kills its group and
   exits 1 (`:39-48`).
3. Child inherits stdin/stdout/stderr (`:51-59`) — the protocol pipe is not
   proxied; SIGTERM → `killGuardGroup` (`-pgid` when the guard leads the group,
   else the direct child only) → exit 143 (`:67-72`); normal exit propagates the
   child's code (`:73-76`).

Remote HTTP: `StreamableClientTransport` + header round-tripper +
`sameHostRedirect` → `oauthHandler` (`connect.go:246`) → `OAuthHandler` only when
non-nil; `clientOptions` (`:108`) points `ToolListChangedHandler` at
`OnToolsChanged`, which drops the set's cached `tools/list`.

## Integration

Consumers (`_test.go` excluded):
- `internal/mcpserver/manager.go:251` `New` — default connector
  `&downstream.Connector{Version: version, Env: downstream.EnvFor}` (`:255`).
- `internal/manager/probe.go:33`, `browse.go:136` — one-off connectors with
  `managerEnv(m.sec)` / `b.env` for health and browse.
- `internal/manager/runtime.go:847-857` `childEnv` — merges `BaseEnv()` and
  `EnvFor(srv)` into the environment of a whole set.
- `internal/manager/oauth.go:118` — connector whose `Authorize` delegates the
  redirect to the panel's HTTP server.
- `cmd/mcprig/main.go:57` `RunChildGuardMode(os.Args)`; `:314`, `:567` — `serve`
  and `stdio` connectors with `OAuth: cfg.OAuth, Store: store, Env: downstream.EnvFor`;
  only `serve` attaches a file tail (`conn.ServerLogs = downstream.NewServerLogs(...)`,
  `:317`), because the panel is what reads it.
- `cmd/mcprig/auth.go:99` — CLI authorization, `SingleAuthorization: true`, a
  `Browser` printing the URL; `cmd/mcprig/warm.go:180` — warm-up connector.
- Tests in `internal/sandbox`, `internal/mcpserver`, `internal/manager` and
  `cmd/mcprig` call `RunChildGuardMode` in `TestMain`.

Dependencies: `github.com/modelcontextprotocol/go-sdk/mcp` (v1.8.0),
`internal/config` (Server/OAuth/transport, `ReservedEnvName`, `ScrubSecrets`),
`internal/sandbox` (`Wrap`), `internal/oauth`. Footprint: no files; spawns
processes, reads `/proc/<pid>/stat` (`procgroup.go:128`). Platform split:
`procattr_linux.go` (Pdeathsig+Setpgid) vs `procattr_other.go` (Setpgid only —
no Pdeathsig, grandchildren survive a SIGKILL of the set).
