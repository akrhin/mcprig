# internal/atomicfile/

## Responsibility

Replaces a file so that a concurrent reader never observes a partial one
(`atomicfile.go:1`). That is the whole package: no locking, no merging, no
retry.

## Design

- **Write** (`atomicfile.go:12-14`): `replace(path, data, sync=true)`.
  **WriteNoSync** (`:20-22`): same without fsync — still atomic (temp + rename),
  but a machine crash may lose the last write; for data that is rebuilt rather
  than read back from the file (token cache, snapshots, metrics), per the doc
  comment at `:16-19`.
- **Contract** (`replace`, `atomicfile.go:24-63`), in order:
  1. `os.MkdirAll(dir, 0700)` — the directory is created if missing (`:26-28`).
  2. `os.CreateTemp(dir, "."+base+".tmp-*")` — the temp file is in the **same
     directory**, which is what makes the rename atomic (`:29-32`).
  3. `defer os.Remove(name)` — a no-op after a successful rename, cleanup on
     every error path (`:34-35`).
  4. `Chmod(0600)` — the result is private regardless of umask (`:36-39`).
  5. `Write(data)` (`:40-43`).
  6. `Sync()` only when `sync` is true (`:44-49`) — the data is durable before
     the rename becomes visible.
  7. `Close()` (`:50-52`).
  8. `os.Rename(name, path)` — the atomic step (`:53-55`).
  9. directory `Sync()` after the rename, best effort: an `ENOTSUP` from the
     filesystem is not a write failure, and neither an `Open` nor a `Sync` error
     is propagated (`:56-63`).
- So the guarantees are: readers see either the old bytes or the new bytes,
  never a prefix; the replaced file is mode `0600`; the containing directory is
  `0700` when created; with `Write`, the new bytes are on disk before the rename
  is visible. There is no fsync of the parent directory that is reported to the
  caller, and no ownership or mode preservation from the old file — a `0o600`
  replacement is deliberate, not inherited.
- Temp file names are `.<base>.tmp-*`, so a crashed writer leaves a dotfile that
  is obviously debris; nothing sweeps those leftovers.

## Flow

`Write(path string, data []byte) error` /
`WriteNoSync(path string, data []byte) error` → `replace` → `nil` or the first
failure (MkdirAll, CreateTemp, Chmod, Write, Sync, Close, Rename). Partial state
is only ever the untouched old file plus, on crash, a stray temp file.

## Integration

- `internal/toollock/toollock.go:138` — every `tools.lock.json` write
  (`Write` → `atomicfile.Write`), under its own flock.
- `internal/registry/store.go:18` — server/set registry files.
- `internal/oauth/store.go:19` — OAuth token store.
- `internal/stats/history.go:31` — stats history files.
- `internal/manager/setconfig.go:13` — set configuration writes.
- External: standard library only (`os`, `path/filepath`); relies on rename
  within one directory, so it must not be used across filesystems.
