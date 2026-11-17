# internal/downstream/

## Responsibility

Opens MCP client sessions to downstream servers — local stdio processes and
remote HTTP endpoints — and owns what must survive that process: env allowlist,
sandbox argv, child-guard supervisor, group teardown, stderr, OAuth wiring.

- `connect.go:22` `Connector` is the entry point; `Connect` (`connect.go:60`)
  returns an `*mcp.ClientSession`.
- `guard.go:23` `GuardMain` is the `mcprig child-guard --` supervisor mode.

## Design

**Connector is a value, not a manager.** Policy arrives through fields, so
`serve`, the panel manager, probe, browse and `warm` each build their own
(`connect.go:22-58`): `Env` (`:31`, nil = caller's environment — the
`manage`/`serve` path), `OAuth`+`Store` (`:24-25`, nil store = flow off),
`Browser` (`:43`), `Authorize` (`:47`, panel login), `SingleAuthorization`
(`:50`), `OnSandboxFallback` (`:35`), `OnSandboxApplied` (`:39`),
`OnToolsChanged` (`:57`).

**Two transports, one switch.** `transport` (`connect.go:118`) serves stdio and
`config.TransportHTTP`. Remote is `mcp.StreamableClientTransport` (`:184`) with
headers injected per RoundTrip (`withHeaders:294`) and a `sameHostRedirect` guard
(`:260`): Authorization would leak to another host on a cross-host redirect, so
those are refused (`:264`). `OAuthHandler` is set only when non-nil (`:197`) —
a typed-nil interface would make the SDK call `TokenSource` on nil.

**Protocol is pinned for stdio only.** `legacyProtocol = "2025-11-25"`
(`connect.go:86`, via `connectOptions:92`): SDK ≤ 1.9 servers lack
`server/discover` (SEP-2575) and die on it; `initialize` is universal.

**Process shape: set → guard → server, each in its own group.** The set's direct
child is `os.Executable()` running `child-guard -- <argv>` (`guardArgv`,
`connect.go:311`); `childAttr` (`procattr_linux.go:10`) sets `Setpgid` plus
`Pdeathsig: SIGTERM`, so the kernel signals the guard even when the set is
SIGKILLed and it outlives the set to kill grandchildren (`guard.go:13-22`). A
replaced binary — `/proc/self/exe` ending in " (deleted)" — skips the guard with
a warning (`connect.go:155-162`, `fileExists:321`).

**Sandbox is an argv prefix, fail-closed.** `sandbox.Wrap` (`connect.go:126`)
returns `Result{Argv, Applied, Reason}`; a deny refusal reports the reason via
`OnSandboxFallback` then fails, so `list_servers` never claims bwrap (`:129-134`).

**Teardown kills groups, not pids.** `groupTransport.Connect` (`procgroup.go:22`)
records `pgid = Command.Process.Pid` (the child leads its own group);
`groupConn.Close` (`:38`) waits up to `closeGrace` 3 s after stdin closes, then
SIGTERMs the group, waits `groupGrace` 1 s, then SIGKILLs (`:47-63`), grace
zeroed when the context already expired (`:42-46`). `groupAlive` (`:105`) counts
members via `/proc/<pid>/stat`, ignoring zombies that `kill(0)` calls alive.

**Env is an allowlist, and reserved names never leave.** `baseEnvKeep`
(`env.go:14`) is `PATH HOME LANG TZ TMPDIR NODE_EXTRA_CA_CERTS`; prefixes
`LC_ XDG_ SSL_` (`env.go:19`) always pass; `EnvAllow` adds named host variables
(`envSelect:38`); manager-owned names are filtered both ways (`withoutReserved`,
`connect.go:283`; `reservedName` → `config.ReservedEnvName`). Server `env` wins
over the inherited base (`connect.go:178`).

**Failures carry the child's own words.** Go sends a child's stderr to /dev/null,
so a bwrap/npx startup failure surfaces as bare EOF; `stderrTail`
(`connect.go:208`) keeps the last 4096 bytes (`:213`) and `Connect` appends them
to the error, scrubbed by `config.ScrubSecrets` (`:76`).

## Flow

Stdio child start (`Connector.Connect` → session):
1. `transport` → `sandbox.Wrap(srv, srv.Command)` (`connect.go:126`); deny →
   `OnSandboxFallback` + error; `Reason` → warn log + `OnSandboxFallback`;
   `Applied` → `OnSandboxApplied` (`:135-147`).
2. `os.Executable()` (`:155`) → `guardArgv` prepends `<exe> child-guard --`
   (`:311`); missing exe → server without guard.
3. `exec.CommandContext(context.WithoutCancel(ctx), …)` (`:165`): context values
   kept, cancellation not — the server outlives the request; plus
   `cmd.Dir = srv.Cwd`, `cmd.SysProcAttr = childAttr()`, `cmd.Stderr = tail`.
4. `cmd.Env = withoutReserved(base) + flatten(srv.Env)`, base being `c.Env(srv)`
   or `os.Environ()` (`:174-178`).
5. `groupTransport{CommandTransport{Command: cmd}}` returned (`:179`); the SDK
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
`sameHostRedirect` → `oauthHandler` (`connect.go:191`) → `OAuthHandler` only when
non-nil; `clientOptions` (`:101`) points `ToolListChangedHandler` at
`OnToolsChanged`, which drops the set's cached `tools/list`.

## Integration

Consumers (`_test.go` excluded):
- `internal/mcpserver/manager.go:252` `New` — default connector
  `&downstream.Connector{Version: version, Env: downstream.EnvFor}` (`:256`).
- `internal/manager/probe.go:33`, `browse.go:136` — one-off connectors with
  `managerEnv(m.sec)` / `b.env` for health and browse.
- `internal/manager/runtime.go:789-800` `childEnv` — merges `BaseEnv()` and
  `EnvFor(srv)` into the environment of a whole set.
- `internal/manager/oauth.go:117` — connector whose `Authorize` delegates the
  redirect to the panel's HTTP server.
- `cmd/mcprig/main.go:57` `RunChildGuardMode(os.Args)`; `:263`, `:513` — `serve`
  and `run` connectors with `OAuth: cfg.OAuth, Store: store, Env: downstream.EnvFor`.
- `cmd/mcprig/auth.go:99` — CLI authorization, `SingleAuthorization: true`, a
  `Browser` printing the URL; `cmd/mcprig/warm.go:184` — warm-up connector.
- Tests in `internal/sandbox`, `internal/mcpserver`, `internal/manager` and
  `cmd/mcprig` call `RunChildGuardMode` in `TestMain`.

Dependencies: `github.com/modelcontextprotocol/go-sdk/mcp` (v1.8.0),
`internal/config` (Server/OAuth/transport, `ReservedEnvName`, `ScrubSecrets`),
`internal/sandbox` (`Wrap`), `internal/oauth`. Footprint: no files; spawns
processes, reads `/proc/<pid>/stat` (`procgroup.go:128`). Platform split:
`procattr_linux.go` (Pdeathsig+Setpgid) vs `procattr_other.go` (Setpgid only —
no Pdeathsig, grandchildren survive a SIGKILL of the set).
