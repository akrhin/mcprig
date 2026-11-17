# internal/safepath/

## Responsibility

Two shape checks for values that come from the request or the config and end up
in a filesystem path: `Name` — one path segment, `Path` — a path to read.

## Design

- `Name(s)` (`safepath.go:20`) is exactly the registry's rule,
  `^[a-z0-9][a-z0-9_-]{0,63}$` (`safepath.go:16`): the same text that gates set
  names and ids. Set names, server names and directory names all pass it or are
  refused.
- `Path(p)` (`safepath.go:25`) demands absolute, no `..` element, no control
  characters. It is meant to run **before** `filepath.Join`: after the join,
  `Clean` has already folded `..` away and there is nothing left to check.
  (For a path built by `filepath.Join` the fold happens at build time — join with
  string concatenation or check the raw value.)
- **Why its own package.** The rule is needed by `internal/registry` (names and
  ids), `internal/manager` (stats directory per set) and `internal/stats` (paths
  built from a set directory). None of them may import another, so the check
  lives below all three and imports nothing but the standard library — no cycle
  in any order.

## Flow

Pure functions: value in, verdict out. Callers decide what a refusal means —
empty string and a log line (`manager.statsDir`), or "file absent"
(`stats.cached`, `sandbox.bind` errors).

## Integration

- `internal/registry/validate.go:142,367`, `internal/registry/store.go:132` —
  server names and set ids.
- `internal/manager/stats.go:83` — `statsDir` returns `""` and logs a warning.
- `internal/stats/history_query.go:439` — `cached` treats a bad path as a missing
  file.
- `internal/sandbox/sandbox.go:173` — mount path after `filepath.Clean`.
