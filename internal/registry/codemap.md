# internal/registry/

## Responsibility
- Owns the manager's durable state: downstream servers, sets (one set = one instance of
  this binary started by the manager, lazy-mcp-compatible), the access keys of every set,
  and the port allocator (`types.go:1-6`). Persisted as one `registry.json`
  (`types.go:17`, `store.go:36`).
- Keeps the TS reference's format with its defects fixed: ports inside 9100–9199, tokens
  never leave through `PublicSet`, `env`/`headers` accept strings only, writes are atomic
  (`types.go:3-5`).
- Validates every write path (`AddServer`, `UpdateServer`, `AddSet`, `UpdateSet`,
  `Import`) with the rules a set applies at start, so the panel cannot store what the set
  would refuse (`validate.go:141-277,343-537`).

## Design
- **One store, one mutex.** `Store{mu sync.Mutex, dir, path, data Data, dropped []string}`
  (`store.go:25-31`); every exported method locks at entry (`store.go:300-305`). Readers get
  copies: `Servers()` copies the slice (`store.go:300-305`), `Sets()`/`Set()` return
  `set.snapshot()` deep-copying `Servers`, `Keys`, `Notify`, `RateLimit.PerTool`,
  `Profiles` (`store.go:420-450`).
- **Write-then-publish.** `commitLocked` writes `next` and only then assigns `s.data =
  next` (`store.go:266-278`): a failed disk write leaves memory as it was, never ahead of
  disk (`store_test.go:585`).
- **Atomic writes, one-generation backup.** `writeLocked` copies the current file to
  `registry.json.bak`, then `atomicfile.Write` (`store.go:280-290`): temp file, fsync,
  rename, 0600 in 0700 (`internal/atomicfile/atomicfile.go:11-13,31-40`). Root is
  `Data{Version, Servers, Sets, NextPort}` (`types.go:87-92`), 2-space indented
  (`store.go:239-241`).
- **Server shape is not duplicated.** `type Server = *config.Server` (`types.go:27`): the
  registry entry *is* the object a set's `servers.json` carries, so validation reuses
  `config.Server.Check()` (`validate.go:276`).
- **Sets** (`types.go:68-84`): `Servers []string` (names), `Port`, `Keys`, `Token` (mirror
  of the primary active key, never sent to clients), `Autostart`, `Open`, `Notify`,
  `RateLimit`, `Profiles`.
- **Keys are plaintext here — no hash is stored.** `Key{ID, Token, CreatedAt, RevokedAt,
  Label, Note, Profile}` (`types.go:32-44`). Hashing is outside: the listener compares
  `sha256(token)` (`internal/httplisten/httplisten.go:84-101`), stats label clients by a
  6-hex fingerprint of `sha256(token)` (`internal/stats/client.go:33-36`). `newKey` =
  12-hex id + 48-hex token from `crypto/rand` (`keys.go:59-61,77-83`); at most
  `MaxActiveKeys = 8` active keys per set (`keys.go:11`, enforced `store.go:577-579`).
- **Revocation is history, not deletion.** `RevokeKey` only stamps `RevokedAt` and keeps
  the token, so the panel still shows which key it was (`store.go:602-637`,
  `types.go:29-31`). Active = empty `RevokedAt`: `activeToken` (primary, first active —
  `keys.go:16-23`), `activeTokens` (`keys.go:26-34`), `activeKeyCount` (`keys.go:39-47`).
  The only token-derived value a client sees is `keyPrefix`, the first 8 chars of the
  primary token (`keys.go:50-56`), as `PublicSetEntry.KeyPrefix` (`types.go:121`,
  `store.go:737`).
- **Legacy single token migrates in place.** `normalizeKeys` drops malformed and duplicate
  keys, back-fills `createdAt`, copies `note` to `label`, and adopts the old set-level
  `token` as key id `"legacy"` when nothing survives (`keys.go:95-119`, `types.go:22`).
- **Strict where the TS reference was lenient.** Names go through
  `safepath.Name` (`internal/safepath`), the shared rule
  `^[a-z0-9][a-z0-9_-]{0,63}$`: server names (`validate.go:142`)
  and set ids (`store.go:132`, `validate.go:350`); it lives in its own package
  because the panel and the stats reader apply it too. `stringMapStrict` refuses non-string
  `env`/`headers` (`validate.go:82-105`); exactly one of `command[]`/`url`
  (`validate.go:223-229`); `cwd` only stdio (`validate.go:246-248`);
  `sandbox`/`permissions`/`expose` go through the set's own `config` parsers
  (`validate.go:182-221`). Refusals are typed — `ErrInvalid`/`ErrPortBusy` with `Is`
  wrappers (`validate.go:20-39`): the API answers 400/409 by type, not by message text.
- **Ports.** 9100–9199 (`types.go:19-20`); `allocPortLocked` walks `NextPort` forward,
  wraps to `FirstPort`, skips ports already in a set or busy in the OS
  (`store.go:684-708`); `portBusy` probes by binding `127.0.0.1:<port>`
  (`store.go:713-728`). An explicit port is also checked against the OS at create
  (`store.go:500-502`) and on change (`validate.go:432-434`): "free in the registry" is
  not "free in the system" (`store.go:692-694`).
- **Import/export.** `Import` takes an `mcpServers` map; a value is an argv array or an
  object, a bare string is refused because a path with spaces broke silently
  (`importexport.go:60-86`); existing names are skipped, never overwritten
  (`importexport.go:35-37`); one bad entry becomes a `Skip` without stopping the rest
  (`importexport.go:39-51`). `ExportSet` renders `{"mcpServers":{…}}` with `${VAR}`
  unexpanded (`importexport.go:88-138`).
- **Load repairs instead of failing.** Missing file → empty; unparsable → renamed to
  `registry.json.corrupt-<unix>` and replaced (`store.go:36-70`). `normalize` fills
  transport defaults, drops sets whose id breaks the path pattern (`store.go:101-140`),
  rebuilds keys and `Set.Token` (`store.go:127-128`). `validateProfiles` drops a broken
  `profiles` block whole, so keys naming it are refused rather than freed
  (`store.go:151-181`); drops are listed by `Dropped()` (`store.go:182-188`).
  `MANAGER_SEED_FILE` fills an empty registry once — closed sets get a fresh key, open
  sets none (`store.go:190-237`). `CanServe` is the start gate: an active key or
  `Open`, nothing else (`types.go:94-98`).

## Flow
- **Read.** `registry.Open(dataDir)` (`internal/manager/api.go:173`) → read file →
  `validateProfiles` → `normalize` → persist normalization once if the canonical form
  differs (`store.go:66-72`) → optional seed → `*Store`.
- **Write one server.** `AddServer` → `decodeObject` → `requiredString "name"` →
  duplicate check → `parseServer` → `clone()` → `commitLocked` (`store.go:337-362`).
  `UpdateServer` marshals the current entry, overlays the patch (absent = keep, explicit
  `null` = delete, name restored), re-parses the merge (`store.go:364-391`,
  `validate.go:279-303`). `DeleteServer` also strips the name from every set, leaving an
  empty set as a legal state (decision 0004 — `store.go:393-418`).
- **Write one set.** `AddSet` → `buildSet(nil, in)`: the id comes from `id`, else
  `name`, with only case and surrounding spaces normalised — a value outside the
  path pattern is refused, never slugged into another id (`validate.go:338-352`);
  then port allocation, `servers` must exist, notify/rateLimit/profiles
  three-state merge, legacy `token` compatibility (`validate.go:343-537`) → port
  conflict check → OS port probe → first key unless `Open` → commit and advance
  `NextPort` (`store.go:496-528`).
  `UpdateSet` repeats it over `cur`, keeping id and keys from the stored set
  (`store.go:530-552`).
- **Keys.** `IssueKey(setID, profile, label)` appends a key — `Label` and the old
  `Note` name both, so a downgraded binary keeps reading the signature — rewrites
  `Set.Token = activeToken(keys)`, returns the plaintext token once (`store.go:570-600`).
  `RevokeKey` stamps `RevokedAt` and reports the remaining active count, zero meaning
  "stop the set" (`store.go:602-637`). `SetKeyNote` writes `Label` and `Note`
  (`store.go:639-667`).
  `PublicSet` is the client-facing projection: no tokens, per-key `PublicKey`,
  `KeyPrefix`, sorted profile names (`store.go:729-760`, `types.go:100-126`).
- **Import.** Names sorted, each validated via `importObject` + `parseServer`, one commit
  for all accepted entries, a failed save returned as a `Skip` with an empty name
  (`importexport.go:21-58`).

## Integration
- Consumed by `internal/manager`: `api.go:173` opens the store, `api.go:917` builds set
  views from `PublicSet`, `api.go:918` reads `ActiveTokens` to show them once,
  `runtime.go:209` and `api.go:1455,1964` gate start/restart on `CanServe`,
  `setconfig.go:94` copies `ActiveTokens` into the set's `transport.authTokens`; `probe.go`,
  `browse.go`, `notify.go` take servers and sets from it.
- Imports `internal/config` (`validate.go:12`, `store.go:19`, `types.go:12`); imported by
  `manager`, `mcpserver` tests and its own tests. `cmd/mcprig/doctor.go:246` reads
  `registry.json` directly, without `Open`, so the doctor never mutates or renames it.
- Disk owned here: `<dataDir>/registry.json`, `registry.json.bak`,
  `registry.json.corrupt-<unix>` (0600 in 0700). Not owned:
  `<dataDir>/sets/<id>/servers.json`, rendered by `manager/setconfig.go:161-171`.
  `MCPRIG_SEED_FILE` arrives as `MANAGER_SEED_FILE`, set by the manager
  (`internal/manager/api.go:171`) — the name this package reads (`store.go:190`).
