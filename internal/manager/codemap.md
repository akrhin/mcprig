# internal/manager/

## Responsibility

The panel: one HTTP API/UI process that owns the server registry, the secrets
chain and one supervised child per set (`mcprig serve --config`). It alone
decides whether a running set is reloaded, restarted or left alone.

## Design

- **Single aggregate root over `Options`.** `Manager` (`api.go:108`) holds `store`,
  `rt *Runtime`, `sec *Secrets`, `auth`, `health *healthCache`, `browse`,
  `routes []route`, `tls *tls.Config` and the OAuth maps `authFlows`/`authWait`
  (`api.go:132-134`); `New(Options)` (`api.go:141`) wires it and `Handler()` returns
  the `Manager` itself (`api.go:269`) — the type *is* the `http.Handler`
  (`ServeHTTP`, `api.go:387`). `Options` (`api.go:41`) comes from `FromEnv`
  (`api.go:65`: port 9091, data dir, host, bind IP, public URL, TLS cert/key pair,
  seed file, allowed hosts, proxy secret, trusted proxies, `AUTH=off`); test seams
  are `healthEvery`, `probe`, `catalog`, `running` (`api.go:128-129`).
- **Routing is an ordered table, errors are typed.** `buildRoutes` (`api.go:578`)
  returns 28 anchored `route{re *regexp.Regexp, methods map[string]handlerFunc}`;
  `handlerFunc` is `func(*request) error` (`api.go:568`), `serveRoute` scans linearly
  (`api.go:634`), ids match `setPattern = ([a-z0-9][a-z0-9_-]{0,63})` (`api.go:576`).
  Handlers return `*httpError` (`api.go:666`) or `*conflictErr` (`runtime.go:154`);
  `fail` (`api.go:680`) maps them to 400/404/409, unknowns to a generic 500.
- **Auth is a four-state machine plus throttling.** `modeNone | modeProxy |
  modePassword | modeProxyPassword` (`auth.go:22-30`) from presence of password
  and proxy secret (`newAuth`, `auth.go:235`); no credentials fails the start
  (`auth.go:274`). Sessions are memory-only (`sessionStore`, `auth.go:101`): 24
  random bytes + HMAC-SHA256, 12 h TTL (`auth.go:24,115-131`), so a restart logs
  everyone out. Login takes 5 failures/minute per IP, at most 2 concurrent scrypt
  hashes, 8 pending (`auth.go:194-202`). `scryptKey` is RFC 7914 on the stdlib
  (`scrypt.go:15`); hash `scrypt$N$r$p$salt$hash` (`auth.go:55`), N=16384 r=8 p=1 (`auth.go:88`).
- **Pre-dispatch gates.** `checkCSRF` (`api.go:712`) rejects `Sec-Fetch-Site:
  cross-site`, compares `Origin` in full scheme/host/port (`api.go:792`), allows
  `Origin: null` only without a session cookie and demands JSON bodies;
  `hostAllowed` (`api.go:839`) is `httplisten.HostAllowed` (empty = loopback).
- **Supervisor: one state machine per set.** `Runtime` (`runtime.go:71`) keeps
  `procs`, `rings`, `locks`, `nonces`; a per-set lock serialises start/stop/
  restart/reload (`setLock`, `runtime.go:129`), each child owns a process group
  (`Setpgid`, `runtime.go:239`, `killGroup` `runtime.go:592`). Readiness 15 s
  polled every 250 ms (`runtime.go:29-30`), 5 restarts with `1s<<n` backoff capped
  at 30 s (`runtime.go:31-32,336-339`), `stopGrace` 5 s SIGTERM→SIGKILL
  (`runtime.go:33,569`), 5 MiB log rotation, 2000-line ring (`runtime.go:35-36`).
- **Reload is a file protocol with a nonce.** `Reload` (`runtime.go:471`) writes
  `servers.json`, stores a random nonce in `reload.request`, signals the leader
  pid only (`killPid`, `runtime.go:602` — stdio grandchildren must survive), then
  polls `reload.status` for that nonce with a higher generation within
  `reloadBudget` 3 s (`runtime.go:40`). `ReloadOutcome` (`runtime.go:412`); a
  refusal is a value, not an error, because the edit is already committed.
- **Set config is generated.** `BuildSetConfig` (`setconfig.go:63`) renders
  `setFile` (`setconfig.go:51`) 0600 into `<dataDir>/sets/<id>/servers.json`
  (`setconfig.go:154`): transport `http` on `/mcp` with `authToken`+`authTokens`,
  `allowAnonymous` for an open keyless set, `clientNames`/`keyProfiles` keyed by
  `stats.Fingerprint`, `toolOutTtl`; `healthMonitor` {60000,120000} and
  `requestTimeout` 120000 are constants (`setconfig.go:94-95`); `${VAR}` stays
  unresolved — the set expands it.
- **Secrets are a chain with a TTL cache.** `Secrets` (`secrets.go:244`): OpenBao
  KV v2 (`secrets.go:107`; `BAO_ADDR`/`OPENBAO_ADDR`, `BAO_TOKEN`, `BAO_MOUNT`
  =secret, `BAO_PREFIX`=mcp) → `env.json` (`secrets.go:35`) → process environment
  (`resolve`, `secrets.go:382`); 30 s cache (`secrets.go:236`), errors never
  cached; `Resolve` fans out 8 at a time in declared order (`secrets.go:310`);
  `config.ReservedEnvName` names are never substituted (`secrets.go:284`).
- **Health is a 15 s single-flight cache.** `healthCache.get` (`health.go:73`)
  loads under a background context so a cancelled browser request cannot poison
  the cache; probe timeout 15 s, outer budget 30 s (`health.go:18-28`).
  `bearerTransport` (`health.go:117`) adds the set token and
  `stats.ServiceCallHeader`, keeping panel calls out of counters.
- **OAuth is a channel handshake.** `authFlow{State,URL,Message}` (`oauth.go:23`) per
  `set\x00server`; `POST .../authorize` answers 202 and connects via
  `downstream.Connector` with an `Authorize` callback (`oauth.go:96-126`) that
  publishes the URL and blocks on a channel keyed by OAuth `state` (`oauth.go:149`);
  public `GET /oauth/callback` (`oauth.go:189`) delivers the code, tokens go to
  `sets/<id>/oauth.json` (`oauth.go:101`), the set restarts (`oauth.go:141`), 10 min cap.
- **Browse and quarantine are bounded pools.** At most 8 live MCP sessions, 15
  min TTL, 60 s call budget (`browse.go:22-26`), refcounted so close/sweep never
  cuts a call in flight (`browse.go:101-103,211-238`); descriptors are compared
  against `tools.lock.json`, divergence shows as `changed` and blocks the call
  (`browse.go:174`, `tools.go:35`); approval rewrites the lock with
  `approvedBy: "panel"` (`tools.go:82`).

## Flow

- **Boot.** `Run` (`api.go:294`): `New` → `lockDataDir` (writes `manager.pid`,
  refuses a second manager, `api.go:370`) → `http.Server` with `ReadHeaderTimeout`
  15 s, `IdleTimeout` 60 s and no `WriteTimeout` (`api.go:307`) → `Autostart`
  (`api.go:279`) → `toolout` cleanup loop → signal → `Shutdown` 5 s.
- **Request.** security headers → panic recovery → `hostAllowed` → `/healthz`
  short-circuit (public; 503 when an `autostart` set is down, `api.go:357`) →
  `checkCSRF` → public `/login`, `/api/login`, `/api/logout`, `/oauth/callback` →
  `auth.check` → `servePanel` (embedded `web.FS()`, `panel.go:16`) → `serveRoute`
  → `fail`.
- **Start.** `startSet` → `Runtime.Start` → `startLocked` (`runtime.go:176`):
  `CanServe`, unresolved `${VAR}` and `portFree` are checked before any write,
  then config build+write, log open, spawn, `waitPort`; failure kills the group
  and returns 409, success clears the notifier state.
- **Crash.** `wait` (`runtime.go:298`) closes `done`, skips restarts for a
  requested stop or exit code 0, emits `set.crashed`, backs off, re-reads set and
  servers through `rt.reload` and calls `startLocked`; exhausted budget emits
  `set.restart_failed` once.
- **Edit.** `PUT /api/sets/<id>` (`api.go:1430`) validates before writing, then
  `needsRestart` (`api.go:1493` — port change or a new `${VAR}`) picks
  `RestartSet` versus `Runtime.Reload`; a refusal becomes `setView.ReloadError`
  inside a 200 body. `apiSetReload` (`api.go:1534`) holds the set lock across the
  running check and, for a stopped set, only rewrites the file. `apiKeyIssue`
  (`api.go:1755`) issues then reloads via `deliverKeys`; the token appears in that
  answer only, and `apiSetAdd` strips client `id`/`token` (`api.go:1407`).
- **Secrets and server edits.** `PUT /api/env/<NAME>` (`api.go:1069`) writes, then
  `restartSetsUsingEnv` (`api.go:1989`) restarts affected running sets in the
  background under `restartTimeout` 60 s (`api.go:38`), skipping sets that would
  lose a variable and naming them in `skipped`. `apiServerUpdate`
  (`api.go:1327`) reloads the using sets, except when `envChanged`
  (`api.go:2032`) forces `restartSetsUsing`; `apiServerDelete` stops them.
- **Reads.** `GET /api/sets/<id>/servers` → `healthCache.get` → `m.probe` →
  `fetchSetHealth` → MCP `list_servers`, then `noteHealth` logs only transitions;
  `GET /api/calls` merges live `/stats` with on-disk history of stopped sets
  (`calls.go:70`); `GET /api/stats` reads `sets/<id>/stats` (`stats.go:25`);
  `POST /api/sets/<id>/prompt` renders a catalog, falls back to `tools.lock.json`
  names and scrubs secrets (`prompt.go:105-122`).
- **Delete and shutdown.** `apiSetDelete` (`api.go:1595`) stops the process, drops
  the health cache, deletes the registry entry and `RemoveAll`s the set directory
  because `servers.json` holds a live bearer token (`api.go:1608`). `Close` cancels
  the manager context, stops the health ticker and closes browse sessions; `Runtime.Close`
  (`runtime.go:724`) kills sets in parallel within one budget, draining webhooks.

## Integration

- **Consumers.** `cmd/mcprig/main.go:224` — `manager.Run(manager.FromEnv(version))`
  is the panel entry point; `cmd/mcprig/doctor.go:438` uses
  `manager.NewSecrets(dataDir)`, `doctor.go:490` `manager.CallbackURL(...)`;
  `internal/registry/sandbox_test.go:12` imports it in tests only.
- **Exported surface.** `Options`/`FromEnv`/`New`/`Run`; `Manager` (`Handler`,
  `Close`, `Autostart`, `RestartSet`, `ServeHTTP`); `AuthOptions`;
  `Runtime`/`NewRuntime`/`RuntimeStatus`/`ReloadOutcome`; `BuildSetConfig`,
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
  tools.lock.json, oauth.json, stats/}`; the panel also cleans tool-out files of
  panel and sets (`api.go:332-338`), and `sets/<id>` is removed with the set.
