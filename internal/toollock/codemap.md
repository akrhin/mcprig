# internal/toollock/

## Responsibility

The per-set quarantine record of approved tool descriptors, `tools.lock.json` —
this package is the only one that knows the format (`toollock.go:1-5`). A file,
not memory: approval survives a set restart, and two processes write it — the set
proxy (spots a change) and the panel (approves).

## Design

- **Format.** `File{Tools map[string]Entry}`, key `server + "/" + tool` (`Key`,
  `:49`; `Split`, `:52-57` — names contain no `/`). `Entry` (`:30-35`):
  `{hash, seen,omitempty, approvedAt, approvedBy}` — `Hash` approved, `Seen`
  current when it differs. Only the set process fills `Seen`: it alone sees
  descriptors, so the hash is not computed in two places. `Path` =
  `<dataDir>/sets/<setID>/tools.lock.json` (`:43-45`).
- **Hash** (`:154-175`): sha256 over a canonical struct `{name, description,
  inputSchema, outputSchema, annotations}` via `json.Marshal` — key order from the
  struct, not upstream bytes (an upstream may reorder schema keys freely).
- **States.** `hash` set, `seen` empty → approved. `seen` ≠ `hash` → changed, calls
  refused until approved. First sighting is written approved, `approvedBy: "auto"`
  (`internal/mcpserver/quarantine.go:46`), so a new server needs no human.
- **Read.** `ReadFile` returns the error (`tools.lock.json не разобран`, `:84`);
  `Read` swallows it after `slog.Warn` (`:66-72`) so a broken file cannot lock the
  set out of listing; shortcut at `:64-65` — a corrupt file loses prior approvals.
- **Write.** `Update(path, fn)` is the single writer: exclusive `flock` on
  `<path>.lock`, read, `fn`, write only if changed (`:96-107`). The lock file is
  separate because the JSON is replaced by rename, so its inode means nothing
  (`:109-110`); dir `0700`, file `0600` (`:111-127`). Without it a late write
  erased another's approval (`:92-95`). `Write` emits indented JSON + newline via
  `atomicfile.Write` (`:130-139`). **Approve** (`:143-149`): only when `Seen` is
  non-empty and ≠ `Hash`; moves `Seen` into `Hash`, clears `Seen`.
- **Message/Short** (`:179-195`): one refusal text everywhere, both hashes at 12
  chars, the lift command `mcprig approve [--set <id>] server/tool`.

## Flow

Observe: `Update`/`Read` → `{Hash, Seen, ApprovedAt, ApprovedBy}` on disk.
Authorise: descriptor → `Hash` → compare with `Entry.Hash` → allow or `Message`.
Approve: `Entry` with `Seen` → `Approve` → `Update`.

## Integration

- `internal/mcpserver/quarantine.go`: `observe` (`:26-67`) records new descriptors
  and `Seen`, clears `Seen` when a descriptor returns to the approved hash
  (`:48-54`) — else `--all` would approve a hash that no longer exists.
  `quarantine` (`:76-104`) is fail-closed: an unreadable or unwritten file refuses
  the call (`:88-94`); a missing file is not an error (`fs.ErrNotExist`).
  `changedTools` (`:136-148`) marks changed tools in `list_commands` instead of
  hiding them; the refusal carries `Meta{"lazyMcpPolicy": "quarantine"}` (`:172`).
- `internal/manager/tools.go:12`, `browse.go:19`, `prompt.go:21` (panel shows and
  approves), `cmd/mcprig/approve.go:17`, `internal/manager/setconfig.go:17`.
- Depends on `internal/atomicfile` and `.../go-sdk/mcp` (`mcp.Tool`,
  `mcp.ToolAnnotations`); `syscall.Flock` makes it Unix-only.
