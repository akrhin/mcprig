# internal/safelog/

## Responsibility

One function for log values: `Value(s)` returns `s` with control characters
removed, so a set name, server name, URL or user agent from the request cannot
forge a log line.

## Design

`Value` (`safelog.go:12`) maps runes through `unicode.IsControl` and drops the
ones that match — `\r`, `\n`, `\t` and the rest of C0/C1. It removes, it does not
replace: the text stays the same shape, only the bytes that end a log record
disappear. `log/slog` writes values as they are, so this is the only guard on the
way to the journal.

Applied at the write site, not at the model: field names and message text stay
untouched, only the value goes through `safelog.Value`.

## Flow

Value in, value out; no state.

## Integration

- `internal/manager/api.go`, `runtime.go`, `setconfig.go`, `notify.go`,
  `stats.go` — set ids, key ids, paths, methods, user agent, origin, host.
- `internal/oauth/handler.go` — server names, URL, expiry.
- `internal/httplisten/httplisten.go` — path, method, remote address, warnings.
- `internal/downstream/connect.go` — server names, failure reason, executable.

Names that already passed `internal/safepath.Name` carry no control characters;
they go through `Value` anyway, so the log site does not depend on which
validation ran earlier.
