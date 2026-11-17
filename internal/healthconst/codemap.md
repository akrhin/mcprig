# internal/healthconst/

## Responsibility

One place for the two numbers that decide how long a downstream health probe
waits and how long its verdict stays fresh: `TTL` and `Timeout`, both 15 s.
Nothing else lives here.

## Design

The package exists to break an import cycle, not to grow. `internal/mcpserver`
probes downstream servers itself, `internal/manager` probes them for the
panel, and both need the same numbers; `internal/manager` imports
`internal/mcpserver` in tests, so the constants cannot live in either of them.
Fifteen lines of constants is cheaper than the cycle.

`Timeout` bounds a single `tools/list` round trip during a health check — not
a tool call. `TTL` says how long a verdict is reused before the next probe.

## Flow

There is no flow: the package is two exported constants and their doc
comments. Every consumer reads them at composition time.

## Integration

- `internal/manager/health.go` — panel health view and the set list badge.
- `internal/mcpserver/manager.go` — the `list_servers` health column.

Both used to carry private copies (`healthTTL`/`healthTimeout`) that a comment
and a test kept in sync; those copies are gone.
