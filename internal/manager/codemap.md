# internal/manager/

## Responsibility

The panel: one HTTP API/UI process that owns the server registry, the secrets
chain and one supervised child per set (`mcprig serve --config`). It alone
decides whether a running set is reloaded, restarted or left alone.

## Design

- **Single aggregate root over `Options`.** `Manager` (`api.go:110`) holds `store`,
  `rt *Runtime`, `sec *Secrets`, `auth`, `health *healthCache`, `browse`,
  `routes []route`, `tls *tls.Config` and the OAuth maps `authFlows`/`authWait`
  (`api.go:135-136`); `New(Options)` (`api.go:143`) wires it and `Handler()` returns
  the `Manager` itself (`api.go:273`) — the type *is* the `http.Handler`
  (`ServeHTTP`, `api.go:391`). `Options` (`api.go:43`) comes from `FromEnv`
  (`api.go:67`: port 9091, data dir, host, bind IP, public URL, TLS cert/key pair,
  seed file, allowed hosts, proxy secret, trusted proxies, `AUTH=off`); test seams
  are `healthEvery`, `probe`, `catalog`, `running` (`api.go:123-131`).
- **Routing is an ordered table, errors are typed.** `buildRoutes` (`api.go:582`)
  returns 28 anchored `route{re *regexp.Regexp, methods map[string]handlerFunc}`;
  `handlerFunc` is `func(*request) error` (`api.go:572`), `serveRoute` scans linearly
  (`api.go:638`), ids match `setPattern = ([a-z0-9][a-z0-9_-]{0,63})` (`api.go:580`).
  Handlers return `*httpError` (`api.go:670`) or `*conflictErr` (`runtime.go:168`);
  `fail` (`api.go:684`) maps them to 400/404/409, unknowns to a generic 500.
- **Auth is a four-state machine plus throttling.** `modeNone | modeProxy |
  modePassword | modeProxyPassword` (`auth.go:26-30`) from presence of password
  and proxy secret (`newAuth`, `auth.go:222`); no credentials fails the start
  (`auth.go:261`). Sessions are memory-only (`sessionStore`, `auth.go:88`): 32
  random bytes + HMAC-SHA256, 12 h TTL (`auth.go:24,102-136`), so a restart logs
  everyone out. Login takes 5 failures/minute per IP, at most 2 concurrent scrypt
  hashes, 8 pending (`auth.go:181-188`). `scryptKey` is RFC 7914 on the stdlib
  (`scrypt.go:15`); hash `scrypt$N$r$p$salt$hash` (`auth.go:55`), parameters read
  from the stored hash rather than fixed in code (`auth.go:49-82`).
- **Pre-dispatch gates.** `checkCSRF` (`api.go:716`) rejects `Sec-Fetch-Site:
  cross-site`, compares `Origin` in full scheme/host/port (`api.go:796`), allows
  `Origin: null` only without a session cookie and demands JSON bodies;
  `hostAllowed` (`api.go:843`) is `httplisten.HostAllowed` (empty = loopback).
- **Supervisor: one state machine per set.** `Runtime` (`runtime.go:73`) keeps
  `procs`, `rings`, `locks`, `nonces`; a per-set lock serialises start/stop/
  restart/reload (`setLock`, `runtime.go:131`), each child owns a process group
  (`Setpgid`, `runtime.go:263`, `killGroup` `runtime.go:616`). Readiness 15 s
  polled every 250 ms (`runtime.go:31-32`), 5 restarts with `1s<<n` backoff capped
  at 30 s (`runtime.go:33-34,360-363`), `stopGrace` 5 s SIGTERM→SIGKILL
  (`runtime.go:35,414`), 5 MiB log rotation, 2000-line ring (`runtime.go:37-38`).
  `Logs` returns `([]string, error)` (`runtime.go:665`): an unreadable file is an
  error, a missing one is an empty tail; `ServerLogs` (`runtime.go:744`) logs that
  error and still returns the matching set lines. `Forget` (`runtime.go:145`)
  drops the proc, ring, lock and nonce of a deleted set, so a new set with the
  same name starts clean.
- **Reload is a file protocol with a nonce.** `Reload` (`runtime.go:495`) writes
  `servers.json`, stores a random nonce in `reload.request`, signals the leader
  pid only (`killPid`, `runtime.go:626` — stdio grandchildren must survive), then
  polls `reload.status` for that nonce with a higher generation within
  `reloadBudget` 3 s (`runtime.go:42`). `ReloadOutcome` (`runtime.go:436`); a
  refusal is a value, not an error, because the edit is already committed.
- **Set config is generated.** `BuildSetConfig` (`setconfig.go:70`) renders
  `setFile` (`setconfig.go:54`) 0600 into `<dataDir>/sets/<id>/servers.json`
  (`setconfig.go:161`): transport `http` on `/mcp` with `authToken`+`authTokens`,
  `allowAnonymous` for an open keyless set, `clientNames`/`keyProfiles` keyed by
  `stats.Fingerprint`, `toolOutTtl`; `healthMonitor` {60000,120000} and
  `requestTimeout` 120000 are constants (`setconfig.go:101-102`); `${VAR}` stays
  unresolved — the set expands it.
- **Secrets are a chain with a TTL cache.** `Secrets` (`secrets.go:245`): OpenBao
  KV v2 (`secrets.go:107`; `BAO_ADDR`/`OPENBAO_ADDR`, `BAO_TOKEN`, `BAO_MOUNT`
  =secret, `BAO_PREFIX`=mcp) → `env.json` (`secrets.go:35`) → process environment
  (`resolve`, `secrets.go:382`); 30 s cache (`secrets.go:236`), errors never
  cached; `Resolve` fans out 8 at a time in declared order (`secrets.go:310`);
  `config.ReservedEnvName` names are never substituted (`secrets.go:284`).
- **Health is a 15 s single-flight cache.** `healthCache.get` (`health.go:73`)
  loads under a background context so a cancelled browser request cannot poison
  the cache; probe timeout 15 s, outer budget 30 s (`health.go:19-30`).
  `bearerTransport` (`health.go:117`) adds the set token and
  `stats.ServiceCallHeader`, keeping panel calls out of counters.
- **OAuth is a channel handshake.** `authFlow{State,URL,Message}` (`oauth.go:23`) per
  `set\x00server`; `POST .../authorize` answers 202 and connects via
  `downstream.Connector` with an `Authorize` callback (`oauth.go:97-143`) that
  publishes the URL and blocks on a channel keyed by OAuth `state`
  (`oauth.go:149`); public `GET /oauth/callback` (`oauth.go:188`) delivers the
  code, tokens go to `sets/<id>/oauth.json` (`oauth.go:103`), the set restarts
  (`oauth.go:141`), 10 min cap (`oauth.go:20`).
- **Browse and quarantine are bounded pools.** At most 8 live MCP sessions, 15
  min TTL, 60 s call budget (`browse.go:22-26`), refcounted so close/sweep never
  cuts a call in flight (`browse.go:98-101,209-236`); descriptors are compared
  against `tools.lock.json`, divergence shows as `changed` and blocks the call
  (`browse.go:315`, `tools.go:35`); approval rewrites the lock with
  `approvedBy: "panel"` (`tools.go:82`).
- **Server log tails belong to the run that wrote them.** `clearServerLogs`
  (`setconfig.go:215`) runs only when `startLocked` starts a set with
  `restarts == 0` (`runtime.go:252-260`) — a manual stop keeps the lines, and an
  auto-restart after a crash appends to the same file under a new launch marker;
  a fresh start truncates the previous run's tails.

## Flow

- **Boot.** `Run` (`api.go:298`): `New` → `lockDataDir` (writes `manager.pid`,
  refuses a second manager, `api.go:374`) → `http.Server` with `ReadHeaderTimeout`
  15 s, `IdleTimeout` 60 s and no `WriteTimeout` (`api.go:311`) → `Autostart`
  (`api.go:283`) → `toolout` cleanup loop → signal → `Shutdown` 5 s.
- **Request.** security headers → panic recovery → `hostAllowed` → `/healthz`
  short-circuit (public; 503 when an `autostart` set is down, `api.go:406-412`) →
  `checkCSRF` → public `/login`, `/api/login`, `/api/logout`, `/oauth/callback` →
  `auth.check` → `servePanel` (embedded `web.FS()`, `panel.go:16`) → `serveRoute`
  → `fail`.
- **Start.** `startSet` → `Runtime.Start` → `startLocked` (`runtime.go:190`):
  `CanServe`, unresolved `${VAR}` and `portFree` are checked before any write,
  then config build+write, log open, spawn, `waitPort`; failure kills the group
  and returns 409, success clears the notifier state. `portFree`
  (`runtime.go:932`) retries the bind five times 200 ms apart: a just-stopped
  set releases its port a few milliseconds late, while a foreign listener is
  still detected within a second.
- **Crash.** `wait` (`runtime.go:322`) closes `done`, skips restarts for a
  requested stop or exit code 0, emits `set.crashed`, backs off, re-reads set and
  servers through `rt.reload` and calls `startLocked`; exhausted budget emits
  `set.restart_failed` once. `stopProc` (`runtime.go:410`) no longer clears server
  tails: the lines explain why a server died.
- **Edit.** `PUT /api/sets/<id>` (`api.go:1442`) validates before writing, then
  `needsRestart` (`api.go:1514` — port change or a new `${VAR}`) picks
  `RestartSet` versus `Runtime.Reload`; a refusal becomes `setView.ReloadError`
  inside a 200 body. `apiSetReload` (`api.go:1555`) holds the set lock across the
  running check and, for a stopped set, only rewrites the file. `apiKeyIssue`
  (`api.go:1795`) issues then reloads via `deliverKeys`; the token appears in that
  answer only, and `apiSetAdd` strips client `id`/`token` (`api.go:1409`).
- **Secrets and server edits.** `PUT /api/env/<NAME>` (`api.go:1073`) writes, then
  `restartSetsUsingEnv` (`api.go:2031`) restarts affected running sets in the
  background under `restartTimeout` 60 s (`api.go:40`), skipping sets that would
  lose a variable and naming them in `skipped`. `apiServerUpdate`
  (`api.go:1339`) reloads the using sets, except when `envChanged`
  (`api.go:2073`) forces `restartSetsUsing`; `apiServerDelete` (`api.go:1386`)
  stops them.
- **Reads.** `GET /api/sets/<id>/servers` → `healthCache.get` → `m.probe` →
  `fetchSetHealth` → MCP `list_servers`, then `noteHealth` logs only transitions;
  `GET /api/calls` merges live `/stats` with on-disk history of stopped sets
  (`calls.go:70`); `GET /api/stats` reads `sets/<id>/stats` (`stats.go:25`);
  `POST /api/sets/<id>/prompt` renders a catalog, falls back to `tools.lock.json`
  names and scrubs secrets (`prompt.go:105-122`). `GET /api/sets/<id>/logs` and
  `…/servers/<n>/logs` put the read failure into the body as `error` next to an
  empty `logs` list (`api.go:1691-1702,1705-1724`), and the path under the data
  dir is shortened before it leaves the panel.
- **Delete and shutdown.** `apiSetDelete` (`api.go:1625`) stops the process, drops
  the health cache, deletes the registry entry, `RemoveAll`s the set directory
  because `servers.json` holds a live bearer token (`api.go:1638`) and calls
  `Runtime.Forget` (`api.go:1641`). `Close` cancels
  the manager context, stops the health ticker and closes browse sessions; `Runtime.Close`
  (`runtime.go:784`) kills sets in parallel within one budget, draining webhooks.

## Integration

- **Consumers.** `cmd/mcprig/main.go:275` — `manager.Run(manager.FromEnv(version))`
  is the panel entry point; `cmd/mcprig/doctor.go:393` uses
  `manager.NewSecrets(dataDir)`, `doctor.go:445` `manager.CallbackURL(...)`;
  `internal/registry/sandbox_test.go:12` imports it in tests only.
- **Exported surface.** `Options`/`FromEnv`/`New`/`Run`; `Manager` (`Handler`,
  `Close`, `Autostart`, `RestartSet`, `ServeHTTP`); `AuthOptions`;
  `Runtime`/`NewRuntime`/`RuntimeStatus`/`ReloadOutcome` (`Logs`, `ServerLogs`,
  `Forget`); `BuildSetConfig`,
  `WriteSetConfig`, `LogPath`, `ReloadStatusPath`, `ReloadRequestPath`;
  `NewSecrets`/`Secrets`; `CallbackURL`.
- **Internal dependencies.** `config` (server model, permissions, profiles,
  `ValidatePublicURL`, `ScrubSecrets`, `ReservedEnvName`), `registry` (store, keys,
  `PublicSet`, `CanServe`), `downstream` (`Connector`, `BaseEnv`, `EnvFor`), `stats`
  (fingerprint, service-call header, rotation, `QuerySets`), `toolout`, `toollock`,
  `atomicfile`, `httplisten`, `oauth`, `web` (embedded panel files).
- **External dependencies.** Only `github.com/modelcontextprotocol/go-sdk/mcp`
  (`mcp.NewClient`, `StreamableClientTransport`, `ClientSession`) in non-test
  code; `golang.org/x/oauth2` appears only in `runtime_statedir_test.go`; no
  OpenTelemetry import.
- **Disk it owns.** `<dataDir>/manager.pid`, `<dataDir>/env.json`,
  `<dataDir>/registry.json` (through `registry`), and per set
  `<dataDir>/sets/<id>/{servers.json, mcprig.log, reload.request, reload.status,
  tools.lock.json, oauth.json, stats/, serverlogs/<server>.log}`; the panel also
  cleans tool-out files of panel and sets (`api.go:336-342`), and `sets/<id>` is
  removed with the set.
