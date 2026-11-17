# internal/config/

## Responsibility
- Reads one set's config file — the lazy-mcp `servers.json` format, kept
  byte-compatible because it is the contract with the processes this binary spawns
  (`config.go:1-4`).
- Holds the policy primitives both sides must agree on: the permissions gate
  (`permissions.go:1-2`), key profiles (`profiles.go`), the rate-limit block
  (`ratelimit.go:10-13`) and secret masking (`secrets.go:8-14`) — the panel's browse path
  and the proxy's `invoke_command` call the same functions.
- Validates each server without failing the load: an unusable entry keeps a
  `ConfigError` and shows up unhealthy (`config.go:27-28,911-938`).

## Design
- **Sources: the file is the only content source.** The path comes from the `--config`
  flag, default `$XDG_CONFIG_HOME/mcprig/servers.json` (`cmd/mcprig/main.go:98,483-486`);
  no flag-over-env-over-file layering exists in this package. Environment enters per value
  only: `${VAR}` from `os.LookupEnv`, `{file:/abs/path}` from disk
  (`config.go:564-602`), and an unset variable or relative path is an error
  (`config.go:575-577,595-597`). References expand in `env`/`headers`
  (`config.go:615-638`) and `transport.authToken` (`config.go:858-867`);
  `transport.authTokens` from the registry is not expanded, but reserved names are
  rejected there too (`config.go:868-876`).
- **Reserved names never travel down.** `MCPRIG_`, `BAO_`, `OPENBAO_` (`config.go:531`) are
  refused as `env` keys, in `envAllow`, and as `${VAR}` in `env`/`headers`/`authToken`, so
  a downstream server cannot receive the panel password or the secret-store token
  (`config.go:543-562,604-613,621-624`).
- **Soft-fail per block, hard-fail per name.** A broken global `rateLimit`, `oauth`,
  `profiles` or `permissions` block is dropped with a `slog` warning and the set keeps
  running (`config.go:880-910`); a server that fails to decode is kept with `ConfigError`
  (`config.go:912-927`), while a server without a name fails the load (`config.go:928-930`)
  and duplicate names mark the later entry (`config.go:920-934`).
- **`Server`** (`config.go:29-51`): `Name`, `Transport` (`stdio`/`http`,
  `config.go:20-25`), `Command []string`, `Cwd`, `Env`, `EnvAllow`, `URL`, `Headers`,
  `Tags`, `Enabled`, `Expose`, `Permissions`, `OAuth`, `Sandbox`, plus `ConfigError`
  marked `json:"-"`. `UnmarshalJSON` accepts the argv array and the legacy
  `"command": "npx"` + `"args"` (`config.go:350-404`); `env`/`headers` decode as
  `map[string]any` through `stringMap`, which records a defect instead of stringifying an
  object (`config.go:390-392,498-514`).
- **`Sandbox`** (`config.go:53-67`): empty block means bwrap, network off, deny when
  unavailable (`config.go:96-125`); defaults 2048 MB memory, 256 pids, 30 s timeout, 128 MB
  tmpfs (`config.go:83-88`); stdio only (`config.go:704-708`). A server tagged `untrusted`
  needs a sandbox with `onUnavailable=deny`, else it does not start
  (`config.go:223-232,715-724`).
- **Permissions model** (`config.go:299-321`): `Permissions{Default, Rules}`,
  `Rule{Server, Pattern, Action, Message}`; `ask` is not implemented (`config.go:299`);
  `rule.server` is legal only in the global block (`config.go:343-345`). Resolution
  (`permissions.go:30-59`): server rules → global rules (rule `server` defaults to `*`) →
  server `default` → global `default` → implicit allow, and within a level the *last*
  matching rule wins (`permissions.go:61-69`); `ParsePermissions` (`permissions.go:12-28`)
  serves both loader and registry API.
- **Matching is glob, not substring** (`permissions.go:71-114`): the pattern compiles to
  `(?is)^…$` with `*` → `.*`, `?` → `.`, everything else `regexp.QuoteMeta`'d; `.` matches
  `\n` on purpose so an upstream tool name with a line break cannot slip past a deny rule
  (`permissions.go:96`); runs of `**` collapse first against backtracking
  (`permissions.go:88-90`); compiled patterns are cached (`permissions.go:80-93`).
- **Profiles narrow, never widen** (`profiles.go:10-40`): `deny` beats `allow`; a pattern
  containing `/` matches `server/command`, otherwise the command name alone
  (`profiles.go:42-47`); empty allow list = "only deny rules apply". A missing profile denies
  everything — a key naming a deleted profile is refused, not freed
  (`internal/mcpserver/permissions.go:56-62`). Parsing is strict: unknown fields (`denny`),
  empty patterns and duplicate names after trimming are errors (`profiles.go:55-109`).
- **`RateLimit`** (`ratelimit.go:15-22`): `PerMinute` per (client, tool), `Burst`
  (defaults to `PerMinute`), `PerTool` keyed `"server/tool"`. `Decode` rejects unknown fields
  — a `perminute` typo would silently remove the limit (`ratelimit.go:24-31`) — and nested
  `perTool` (`ratelimit.go:57-59`); a zero block is stored as the field's absence
  (`ratelimit.go:64-79`). It lives in the registry entry, is copied into the set's
  `servers.json`, and the proxy enforces it where calls happen (`ratelimit.go:10-13`).
- **Secrets** (`secrets.go:10`): every `env`/`header` whose name matches
  `key|token|secret|pass|auth|credential` contributes its value if at least 8 chars, with a
  leading `Bearer ` stripped (`secrets.go:15-37`); `ScrubSecrets` replaces them with `***` in
  text that leaves the process — child stderr, upstream errors (`secrets.go:39-47`), applied
  by `internal/manager/prompt.go:110,117-120,184`. What a client sees carries fingerprints,
  not tokens: `clientNames` (6-hex `sha256` → label) and `keyProfiles` (`config.go:758-768`).
- **`Check()` validates a copy.** `Server.Check` runs `validateSandbox` on a shallow copy
  with a copied `Sandbox`, so `setDefaults` and `ConfigError` never mutate the shared
  registry object (`config.go:728-743`); `Config.Usable()` is the serve gate — no
  `ConfigError` and `Enabled` (`config.go:956-967`).

## Flow
- **Read.** `Load(path)` (`config.go:840`): read file → unmarshal the top-level shape
  with raw `permissions`/`rateLimit`/`profiles` (`config.go:845-857`) → expand
  `transport.authToken` or refuse a reserved `${VAR}` (`config.go:858-876`) → soft-parse
  the four global blocks (`config.go:877-910`) → per server: `json.Unmarshal` (defect →
  `ConfigError`, `config.go:912-927`), `expandSecrets` (`config.go:935`), `validate`
  (`config.go:936`) → `*Config`.
- **Validate one server.** `validateSandbox` (`config.go:650-726`) fills `ConfigError` in
  order: missing name, reserved env name, unknown `expose` kind, transport inference and
  type rules, `cwd` outside stdio, sandbox defaults/limits and stdio-only, `untrusted`
  without sandbox, `untrusted` with `onUnavailable != deny`. Panel and browse are not
  trusted either: they build a `config.Server` and ask `Check()`, which is why `probe`/
  `browse` and the set accept the same entries (`config.go:728-731`,
  `internal/manager/browse.go:277`).
- **Apply policy on a call.** `mcpserver.gate` (`internal/mcpserver/permissions.go:51-64`):
  `config.DenyReason(global, server, server, command)` first, then the caller's profile from
  `cfg.Transport.KeyProfiles[stats.ClientKey(ctx)]`; no entry = "key without a profile",
  unrestricted by profile (`permissions.go:31-41`).
- **Write.** This package never writes; `manager.WriteSetConfig`
  (`internal/manager/setconfig.go:153-159`) renders registry state into
  `<dataDir>/sets/<id>/servers.json` atomically, 0600, `${VAR}` unexpanded
  (`setconfig.go:21`); the spawned process reads it back via `Load`.
- **Secret path at start.** `Server.Secrets()` (`secrets.go:15-37`) collects the values and
  the process scrubs child output with them; `ReservedEnvName` (`config.go:534`) filters what
  the child may receive in `env`/`envAllow` (`internal/downstream/env.go`).

## Integration
- Imported by `internal/registry` (`validate.go:12`, `store.go:19`, `types.go:12` —
  `Server` is an alias of `config.Server`, `RateLimit`/`Profile` are embedded in set
  entries) and `internal/manager` (`api.go:28`, `setconfig.go:14`, `runtime.go:23`,
  `browse.go:16`, `oauth.go:13`, `prompt.go:19`).
- Imported by the proxy side — `internal/mcpserver` (`manager.go:27`, `permissions.go:11`,
  `ratelimit.go:11`, `resources.go:14`, `toolout.go:17`), `internal/downstream`
  (`connect.go:15`, `env.go:8`), `internal/sandbox` (`sandbox.go:21`).
- `cmd/mcprig`: `serve`/`stdio` load the flag-given path (`main.go:250,364`), `access` and
  `warm` load their own (`access_test.go:38`, `main.go:501`), `doctor` loads the file
  read-only and reports per-server `ConfigError` (`doctor.go:277`).
- Disk in scope: the file the caller names (default
  `$XDG_CONFIG_HOME/mcprig/servers.json`) plus any absolute `{file:…}` it reads.
  `sets/<id>/servers.json` is written by `internal/manager/setconfig.go`, `registry.json`
  owned by `internal/registry`.
