# internal/toolout/

## Responsibility

Owns the `tool-out` directory: where truncated answers are saved, how long they
live, and the sweep (`toolout.go:1-2`). **It does not truncate** — the cut lives
in `internal/mcpserver/toolout.go`; this package supplies the directory, the
`.owner` sidecar and the retention policy.

**Why the names coincide.** `toolout` here and `toolout.go` there are both about
the same `tool-out` directory: this package ages the files out, that file writes
them. Renaming the package was rejected — 9 importing files, 24 call sites and 34
doc mentions for no behaviour change — so the split is named in both places
instead.

## Design

- **Layout.** `Base()` = `$MCPRIG_DATA_DIR` or `os.TempDir()` (`:47-52`);
  `Dir(base)` = `<base>/tool-out` (`:55`). A set's is `<base>/sets/<id>/tool-out`;
  `dirs` returns both because the panel sweeps the sets it supervises (`:138-144`).
- **Defaults** (`:18-30`): `DefaultTTL = 24h`, `DefaultMax = 256<<20`
  (268435456 bytes per directory), `Interval = 10m` (measured ~2 MiB/h per chatty
  server — hourly let the cap overshoot), `minAge = 5m`.
- **Env** (`FromEnv`, `:59-83`): `MCPRIG_TOOLOUT_TTL` (`time.ParseDuration`) and
  `MCPRIG_TOOLOUT_MAX` (`ParseSize`, `:93-114`: B/K/KiB/M/MiB/G/GiB, binary,
  overflow guard `(1<<62)/mult`). A bad value comes back as a warning, default wins.
- **Ownership.** `AnonymousOwner = "anonymous"` (`:40`); `OwnerPath(dump)` =
  `<name>.owner` (`:44`), one line holding the caller's key fingerprint.
- **Sweep** (`Sweep` → `sweepDir`, `:124-199`): skip dirs and `*.owner`
  (`:161-163`, the sidecar dies with its `.txt`); files older than `TTL` go with
  their `.owner` (`:169-174`); over `Max`, oldest first but nothing younger than
  `minAge` — a client may still be about to read it (`:181-197`).
- **Run** (`:203-222`) sweeps immediately then on a ticker (`Interval` when
  `<= 0`), logs removals, closes the channel on exit. **Usage** (`:236-266`) sums
  size/count across `dirs(base)` and returns the oldest mtime; `fs.ErrNotExist`
  when there is no `tool-out`.
- **The cut itself** (`internal/mcpserver/toolout.go`): `defaultMaxBytes = 32768`
  (`:40`), `maxAllowedBytes = 1048576` (`:42`). `truncateResult` (`:60-108`) streams
  through `spillWriter`, keeping `maxBytes + utf8.UTFMax` in memory (`:63`); an
  answer that fits is returned untouched. On overflow: `<16 hex>.txt`, `0600`,
  `O_EXCL`, `.owner` written **before** the body (`:392-401`); marker
  `\n[обрезано: %d из %d байт, полный ответ — %s]` on a rune boundary (`:95-98`),
  plus a structured-content line (`:101-103`). `toolOutRef` (`:257-264`): on-disk path
  for stdio, `/tool-out/<name> (тот же ключ, которым вызван инструмент)` when
  `m.ToolOutURL` is set — served at `internal/httplisten/httplisten.go:191` by
  `toolOutHandler` (`:230-266`): name `^[0-9a-f]{16}$` (`:215`), `.owner` must
  match the reader (`:238-242`, else 404), past TTL → 410 (`:254-257`).

## Flow

`FromEnv` → `Run`; `Sweep(base, cfg, now) → (Report{Removed, Freed}, error)`;
`Run(ctx, base, cfg, interval, log) → <-chan struct{}`; `Usage(base) → (size, count,
oldest, fs.ErrNotExist)`.

## Integration

- `cmd/mcprig/main.go:202-208` (`startToolout`) sweeps in stdio mode;
  `internal/manager/api.go:332-336` sweeps for the panel and its sets;
  `cmd/mcprig/doctor.go:662-676` prints the `tool-out:` line.
- `internal/mcpserver/toolout.go` writes dumps into `toolout.Dir` + `.owner`;
  `internal/httplisten/httplisten.go` reads them back;
  `internal/manager/setconfig.go:42,:70` ties export lifetime to the same TTL.
- External: filesystem only (`os.ReadDir`, `os.Remove`).
