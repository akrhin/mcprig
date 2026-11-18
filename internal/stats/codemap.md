# internal/stats/

## Responsibility

Per-client call accounting for one set process, in two tiers: **memory** —
counters per `(client, server, tool)` and a ring of the last calls, read by the
panel through the set's `GET /stats` (`stats.go:97` `Recorder`); **disk** —
every call mirrored to a JSONL journal plus day/month aggregates, so a period
breakdown survives a set restart (`history.go:3-14`).

A "client" is a stdio harness name, or for HTTP a key fingerprint plus the
`User-Agent` product token (`client.go:105`). Tokens are deliberately not counted:
MCP answers carry no standard usage field (`stats.go:8-9`). Calls the panel makes
to its own set are counted nowhere (`client.go:79-101`).

## Design

**Recorder (stats.go).** `Recorder` is mutex-guarded: `Record` cleans both
externally supplied labels, computes `ms`, fills a `Call` (`stats.go:145-203`), and
a fixed label from `SetClientLabel` overrides the caller's own (`stats.go:137,159-162`,
used by stdio). Limits, all on external input:

| limit | value | effect |
| --- | --- | --- |
| `MaxLabelBytes` | 64 | client and tool labels, trailing rune repaired (`stats.go:27,61-62`) |
| `MaxCounters` | 4096 | real triples ≤ 4095, overflow collapses into `(прочее)` (`stats.go:32,169,178`) |
| `MaxLatencySamples` | 256 | per-counter reservoir, overwritten round-robin → percentiles describe recent traffic (`stats.go:22,183-188`) |
| `MaxMessageBytes` | 512 | error text kept in the tail ring (`stats.go:37,42`) |
| tail ring | 512 | `NewRecorder(set, 0)` means 512 (`stats.go:122-134`) |

`P50`/`P95` are computed only while building a `Snapshot`, not per call
(`stats.go:303-304,325-327`); `Snapshot` sorts counters by client, server, tool and
returns the tail newest-first (`stats.go:286-323`). `SnapshotRedacted` replaces
client labels with `(аноним)` and drops error texts (`stats.go:305-319`).
`RecordThrottled` counts calls the rate limiter refused
(never seen by an upstream) per `server/tool` (`stats.go:214-226`); `SetSchema`
carries the `list_commands` weight in bytes and tokens at 4 bytes per token
(`stats.go:266-272`).

**Client identity (client.go).** Context carriers only: label, key fingerprint,
address, service-call mark. `Fingerprint` is 6 hex of `sha256(token)`, so the token
never reaches stats or a set config; it stays separate from the display label
because the rate limiter buckets by it — a caller cannot mint quotas by renaming
itself (`client.go:33-46`). `ServiceCallHeader` is `X-MCPRig-Caller: panel`; a key
holder could send it to hide its own calls — an accepted shortcut
(`client.go:89-92`).

**Disk format (history.go).** One writer goroutine is fed by a bounded channel
`queueSize = 1024`; a full channel drops the line and bumps `dropped` (surfaced as
`stats.dropped`), so a tool call never waits for disk
(`history.go:42,172-184,196-229`).

`cutRune` (`history.go:111`) is the one byte-limit helper: it cuts on a rune
boundary, and `cleanErrText` (`history.go:122`), `stats.cleanMessage` and
`stats.CleanLabel` call it instead of repeating the loop (`stats.go:41,54`).

| file | contents | retention |
| --- | --- | --- |
| `raw-YYYY-MM-DD.jsonl` | one `rawLine` per call: `{time, client, server, tool, ms, failed, err}`; `err` single-line, ≤ 300 bytes (`history.go:96-128`) | today + 7 previous days |
| `day-YYYY-MM-DD.json` | `dayFile{day, triples}`; `Triple` = `{client, server, tool, calls, failures, totalMs, hist, lastMs}` (`history.go:82-91,130-135`) | 30 days, then folded into the month |
| `month-YYYY-MM.json` | `monthFile{month, days map[date][]Triple}` — per-day triples, so a repeated fold overwrites instead of doubling (`history.go:137-142`) | forever |

`hist` has 14 fixed buckets, bounds `1,2,5,10,20,50,100,200,500,1000,2000,5000,10000,∞`
(`history.go:52-55`), and a percentile taken from it is the bucket midpoint
(`history.go:69-79`). `Rotate` is idempotent: completed days get a day file
(skipped when one exists), days older than `DayDays-1` fold into their month and
are removed, raw journals older than `today-7` are deleted (`history.go:267-321`).
It runs at start and every `rotateEvery = 24h` (`history.go:44,196-216`), and per
set directory when the panel starts (`internal/manager/api.go:195-204`).
`TailFromDir` reads raw today..today-6 for a stopped set: tail plus
exact-percentile counters (`history.go:401-444`).

**Period queries (history_query.go).** `Query{Period, Bucket, Server, Tool,
Client}`; periods `today`, `7d`, `30d`, `archive` (`history_query.go:15-30`).
One `bundle` accumulates totals, tools, servers, clients, series and by-set rows
in one pass over the files (`history_query.go:202-291`). `today` and `7d` read raw
journals and give exact percentiles from a value→count map (`approx=false`); `30d`
reads day files (raw for unfolded days) and `archive` reads month files plus
unfolded day files, both reporting bucket midpoints (`approx=true`)
(`history_query.go:372-411,159-199,510-568`). Series step is `hour` by default
for today/7d, `day` for 30d, `month` for archive; `bucket=hour|day|month`
overrides it (`history_query.go:417-425`). `bySet` rows exist only through
`QuerySets` (each directory carries its set name), and a set with no calls is
omitted, not zeroed (`history_query.go:237-242,341-347`). Parsed files are cached
by `mtime`+`size` under a global mutex; at `cacheMax = 4096` the oldest quarter
by the files' own `mtime` is evicted before the new entry goes in, so fresh
entries survive the overflow instead of the whole map being dropped
(`history_query.go:419-480`); files carry no set name — `rawLine`/`Triple` have no
such field, the set comes from the `Source` (`history_query.go:239-242`).

## Flow

1. `internal/mcpserver/manager.go:1173` records a real tool call as
   `m.Stats.Record(label, server, tool, duration, err)`; `manager.go:688` records
   the proxy's meta-tools on the pseudo-server `"mcprig"`. Both first check
   `stats.ServiceCall(ctx)`; identity (key fingerprint, service-call mark, caller
   address, client label) is in the request context from `httplisten.go:318-332`.
2. `Recorder.Record` updates counters, reservoir and tail ring, then hands the
   line to `History.Record` (non-blocking enqueue) and returns; the history
   goroutine appends it to the file of the call's own day, switching at midnight
   (`history.go:232-248`).

Outward:

- `GET /stats` on the set → `SnapshotRedacted` for every caller, and the full
  `Snapshot` only for the panel's service call (`X-MCPRig-Caller: panel`),
  JSON (`httplisten.go:187-202`).
- Panel `GET /api/calls` polls running sets' `/stats` in parallel with a 2 s
  timeout, stopped sets through `TailFromDir`, redacting an open set's disk tail
  (`internal/manager/calls.go:66-129`); `GET /api/stats` answers `QuerySets` over
  `sets/<id>/stats` with `bySet` and `unreachable`
  (`internal/manager/stats.go:25-80`).

## Integration

Consumers: `internal/mcpserver` (Recorder owner, `Record`, `SetSchema`,
`RecordThrottled`), `internal/httplisten` (`/stats`), `internal/manager` (`stats`,
`calls`, `api`, `health`, `setconfig`) and `cmd/mcprig`.

Directories on disk: a managed set writes `<data-dir>/sets/<id>/stats/`
(`internal/manager/stats.go:78`); a standalone `serve` writes `<dir of
servers.json>/stats` (`cmd/mcprig/main.go:277-284`); stdio writes
`<data-dir>/stdio/stats` with the fixed client label `stdio`
(`cmd/mcprig/main.go:490,527`). Files are `0600`, the directory `0700`; beyond the
standard library the only dependency is `internal/atomicfile` for day/month writes
(`history.go:31,442-448`), tests sitting beside the sources.
