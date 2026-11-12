This file describes what the panel shows, the API behind it, and how access to
both is checked.

# Panel

`mcprig panel` serves the registry and a single-page dashboard from the same
binary. The UI is embedded (`web/`), has no build step, and runs with a strict
CSP: no inline scripts or styles.

![mcprig panel](../../design/ui/panel.png)

## What it shows

| area | contents |
| --- | --- |
| top bar | breadcrumbs `mcprig / <set>`, the endpoint with a copy button, colour scheme, language, user |
| left rail | all sets as name plus `:port · state` and a state dot; **+ New set** |
| set head | name, port chip, state badge, **Keys**, **Restart**, **Start/Stop**, the key prefix and — right under it — the number of servers and tools plus the `list_commands` weight (bytes, tokens) |
| tabs | **Overview**, **Calls**, **Statistics** |
| KPIs | calls, error rate, p50 and p95 of the selected set over the last 7 days |
| servers of the set | name with a second line (daemon error, isolation, description), type, health, tool count, last request, **Check** and **Edit** |
| calls table | time, server, tool, client, milliseconds, status; search box and **errors only**. Meta-tools (`list_servers`, `list_commands`, `describe_commands`) appear here too, as server `mcprig`; a failure shows its reason next to the status, the full text in a tooltip |
| details drawer | host registry, variables and the set log; opens from the header and covers the panel width on a phone |
| footer | `N sets · M servers · K running` and the last refresh time |

Health is shown as it is: `Healthy`, `Failed` with the daemon's reason on the
second line, and `Not checked` for a server the set has not probed yet. While a
health answer is on its way the cell keeps a skeleton bar instead of guessing.
The tool count of one server is not part of the health answer, so the **Tools**
column shows `—` until **Check** probes that server; the head of the set shows
the total for the whole set.

Everything on the page comes from the panel API below plus `GET /stats` of the
running sets. Nothing else is stored in the browser but the chosen colour scheme
and the interface language.

## Server editor

The **servers** tab of the details drawer and the **Add server** button open a
form over the panel: name, description, transport, command (one argument per line) or
`url` and headers, working directory, `env`, `envAllow`, tags, the `enabled`
switch and a **sandbox** fieldset — `mode`, `onUnavailable`, `network`, `ro` and
`rw` (one path per line), `memoryMb`, `pids`, `tmpMb`, `timeoutSec`. It writes
through `POST /api/servers` and `PUT /api/servers/<name>`; a backend refusal is
shown in the form as its own text and the stored record stays untouched.

The same form is the only way to edit a `sandbox` block from the UI. Fields and
their meaning are in [configuration.md](configuration.md#sandbox); the panel
does not validate them itself — it shows what the backend answers, including
`sandbox: неизвестные поля: …`, `sandbox допустим только для stdio` and
`тег untrusted требует sandbox: сервер без изоляции не стартует`.

The registry list in the drawer also shows isolation, on the second line of a
row: `bwrap` when the sandbox is applied, `no isolation` in warning colour when
the block was requested but bubblewrap did not start (the daemon's full reason
is in the tooltip), and `config rejected` in error colour for a record with
`_configError`. A valid server without a `sandbox` block carries no marker. The
value comes from the same `isolation` field MCP clients see in `list_servers`.

The **Keys** dialog lists key ids, creation and revocation dates and notes. It
shows only the 8-character prefix. The note of an existing key is edited in
place: the row has a label field and a **Save** button that appears once the
value differs from the stored one; the panel sends `PATCH` with `{"note":…}`,
redraws the list and shows a `restartError` of the set as its own warning rather
than swallowing it. A newly issued token is displayed once, and
so is the token of a newly created closed set: `POST /api/sets` returns it in
the `201` body, while `GET /api/sets` never repeats it. Issuing a key later
revokes the previous active key and restarts the set if it is running; revoking
the active key stops the set.

## Themes

The panel ships four colour schemes — dark (default), light, Nord and Gruvbox —
behind one set of CSS variables: colours, radii, the 4 px spacing step and the
type scale. The theme menu sits in the header and switches the `data-theme`
attribute on `<html>` without a reload; the choice is stored in
`localStorage['mcprig.theme']`. The names kept from earlier versions still work
and are rewritten to the new ones on first paint: `mcprig` and `tokyo` mean
dark, `solarized` means light. The dark palette lives in `themes.css` on
`:root`, so an unknown name falls back to it; the CSP is unchanged (no inline
styles).

## Language

The interface speaks English and Russian. English is the default: the `EN`/`RU`
switch sits in the header next to the colour scheme, the choice is stored in
`localStorage['mcprig.lang']`, `<html lang>` follows it, and numbers and dates
are drawn by `Intl` in the current locale (`en-US`/`ru-RU`). Every panel string
lives in one `DICT` in `web/app.js`; `index.html` carries only the English values
and `data-i18n` keys. Strings that come from the daemon — a server error reason,
the `isolation` field, refusals shown in the form — are displayed as they are.

## Routes

Static panel files are served for `GET`/`HEAD` after authentication. The API:

| method and path | purpose |
| --- | --- |
| `GET /healthz` | public liveness probe |
| `GET /` | panel (redirects to `/login` when auth is on and there is no session) |
| `GET /login`, `POST /api/login`, `POST /api/logout` | session login |
| `GET /api/state` | servers, sets, `firstPort` — the first paint |
| `GET /api/sets`, `POST /api/sets` | list and create sets; a new closed set returns its token once in the `201` body |
| `PUT /api/sets/<id>`, `DELETE /api/sets/<id>` | update (restarts a running set) and delete (stops it) |
| `POST /api/sets/<id>/start`, `/stop`, `/restart` | lifecycle |
| `GET /api/sets/<id>/servers` | per-server health for one set |
| `GET /api/sets/<id>/logs?lines=N` | log tail (default 200 lines) |
| `GET /api/sets/<id>/servers/<name>/logs` | one downstream's log lines |
| `POST /api/sets/<id>/keys` | issue a key; the response contains the only copy of the token |
| `DELETE /api/sets/<id>/keys/<keyID>` | revoke a key; revoking the active key stops the set and the answer carries `stopped: true` |
| `PATCH /api/sets/<id>/keys/<keyID>` | sign a key: `{"note":"hermes"}`; the note becomes the client's label in the statistics, an empty string clears it, a running set is restarted |
| `GET /api/servers`, `POST /api/servers` | list and add downstream servers |
| `PUT /api/servers/<name>`, `DELETE /api/servers/<name>` | update and delete; running sets are restarted. Deleting the last server of a set answers 400 (`сервер X последний в наборе Y`) — remove the set instead |
| `POST /api/servers/<name>/probe` | `initialize` + `tools/list`, 60 s budget, error text in the body |
| `GET /api/calls?set=<id>&limit=<N>` | merged call tail of running sets; default 50, max 500; for one set it also carries `schema` |
| `GET /api/stats?set=<id\|all>&period=today\|7d\|30d\|archive&server=&tool=&client=&bucket=hour\|day\|month` | call history from disk: totals, series, per-tool, per-server and per-client breakdown, `bySet` for all sets |
| `GET /api/env`, `PUT /api/env/<NAME>`, `DELETE /api/env/<NAME>` | variables: names, set flag and source only — never values; the answer returns at once, sets using the variable restart in the background (60 s budget) and `restarted` lists them; stopping the panel cancels the background restart |
| `POST /api/import` | import an `mcpServers` object; returns `added` and `skipped` |
| `GET /api/export/registry`, `GET /api/export?set=<id>` | export the registry or one set |
| `POST /api/browse` | open a live session to a server for tool inspection |
| `GET /api/browse/<id>`, `DELETE /api/browse/<id>` | read or close that session |
| `POST /api/browse/<id>/{tool,resource,prompt}` | call something in the browse session |

Set and server names must match `^[a-z0-9][a-z0-9_-]{0,63}$`. A set needs at
least one existing server; its `port`, when given, must be 9100–9199 and free
in the system, otherwise the manager allocates one; a port held by a process
gives `409` "port N is taken by another process". `id` and `token` in a create body
are ignored.

The set log tail is `<data-dir>/sets/<id>/mcprig.log`, rotating to `.log.1` at
5 MiB. A `lazy-mcp.log` inherited from the old proxy is renamed on the set's
first start, so the old tail stays visible in the panel. A failed rotation
(rename or reopen) loses no output: the old file stays in place and the error
text is shown in the set status `error`.

`/api/calls` polls each running set's `/stats` with a 2 s timeout and its
registry key. A set that does not answer is listed in `unreachable` by id; its
error text is not forwarded.

An answer with `?set=<id>` carries a `schema` field — what the set's tool list
costs in context: `schemaBytes` (the length of the JSON `list_commands` returns
for the whole set), `schemaTokens` (the same length divided by 4, rounded up)
and `tools` (tool count). The numbers come from the set's `/stats` snapshot,
i.e. its tool cache, with no calls to downstream servers. The merged answer
without `set` has no such field: the weight is per set. It is absent until the
set has filled its cache once. The panel prints it in the card head as
`list_commands`: `20 КБ · 5 067 ток · 22 инстр.`

## Call history

Counters of `/api/calls` live in the set's memory and die with the process. The
history below is kept on disk by the set itself, next to its config: for a set
that is `<data-dir>/sets/<id>/stats/`, for a standalone `serve` — the directory
of the `servers.json` it was started with. Files are `0600`, the directory
`0700`.

| file | contents | kept |
| --- | --- | --- |
| `raw-YYYY-MM-DD.jsonl` | one line per call: `{time, client, server, tool, ms, failed, err}`; `err` is the failure reason, a single line of at most 300 bytes, absent on success | today + 7 days |
| `day-YYYY-MM-DD.json` | per `(client, server, tool)`: `{client, server, tool, calls, failures, totalMs, hist, lastMs}` | up to 30 days |
| `month-YYYY-MM.json` | the same triples per day under `days` — a repeated fold overwrites a day instead of doubling it | always |

`hist` is a fixed histogram in milliseconds: `1, 2, 5, 10, 20, 50, 100, 200,
500, 1000, 2000, 5000, 10000, ∞`.

Rotation and cleanup run when the set starts and then once a day: a finished day
gets its day file built from the raw journal, days older than 30 move into their
month file, and journals older than 7 days are removed. Writing never blocks a
tool call: the line goes to a bounded channel served by one goroutine, and a
full channel drops the line, which is reported as `stats.dropped` in the set's
`/stats`.

`GET /api/stats` answers one period:

```text
period=today    from 00:00 today       exact percentiles from raw, approximate=false
period=7d       last 7 calendar days   exact, approximate=false
period=30d      last 30 calendar days  from the histogram, approximate=true
period=archive  older than 30 days     from month files, approximate=true
```

`bucket=hour|day|month` picks the series step (default follows the period): today
and 7d answer hourly (or daily with `bucket=day`), 30d answers daily, archive
monthly. An unknown `bucket` is `400`. `server`, `tool` and `client` filter every part of the answer. `set=all`
(or no `set`) sums all sets. Parsed history files are cached: a file is re-read
only when its `mtime` or size changes, so a repeated query for the same period
touches no disk. The answer is `period`, `from`, `to`, `approximate`,
`totals` and `series` (`{t, calls, failures, p50ms, p95ms}`), plus `tools`,
`servers` and `clients` breakdowns of the same shape. `set=all` (or no `set`) adds `bySet`:
`{set, calls, failures, totalMs}` per set with calls in the period, which the
set table shows without opening one; a set with no calls in the period is left
out rather than reported as zero. A single `set` answer has no `bySet`. The set
filter of the **Statistics** tab follows the rail: an open statistics view shows
the selected set, switching the set switches it too, and a manual choice in the
list lasts until the next rail switch. A set whose stats
directory is missing is listed in `unreachable` by id, with an empty answer
otherwise; an unknown set id is `404`.

## Health

| endpoint | response |
| --- | --- |
| `GET /healthz` (panel) | `{"status":"ok","sets":N}`, or `503` with `status: "degraded"` when an `autostart` set is not running |
| `GET /healthz` (set) | plain `ok` |
| `GET /stats` (set) | JSON snapshot for the panel; `Authorization` required unless the set is open |

The panel's own health view (`GET /api/sets/<id>/servers`) uses a 30 s load
budget — twice the set's 15 s probe timeout — so a set that is still warming is
not reported as `context deadline exceeded` for every server at once, and the
answer is cached for 15 s with a single-flight guard, so opening the dashboard
does not multiply probes across browser tabs.

Both panel health endpoints are public so a container or systemd check can use
them without credentials. The panel's `/healthz` contains no set names.

A set starting or stopping, and a server turning unhealthy, each write one line
to the panel's stdout (`набор <id>: запущен|остановлен`, `набор <id>: сервер
<name> unhealthy`), which systemd collects. An unhealthy transition is logged
only when the state changes, not on every health poll. The same poll runs in the
background every 30 s for every running set, so lines appear even with no panel
open.

## Access

Modes come from `MCPRIG_PASSWORD`, `MCPRIG_PROXY_SECRET` and `MCPRIG_AUTH=off`;
see [cli.md](cli.md#panel). There is no built-in TLS: put the panel behind a
reverse proxy, or keep it on loopback.

```text
none      MCPRIG_AUTH=off
password  MCPRIG_PASSWORD or MCPRIG_PASSWORD_HASH
proxy     MCPRIG_PROXY_SECRET + MCPRIG_USER_HEADER
both      password and proxy secret
```

With password auth the panel sets `mcp_session` (HMAC-SHA256, 12 h, `HttpOnly`,
`SameSite=Lax`, `Secure` over TLS) and keeps sessions in memory. Login is
limited to 5 failures per minute per IP and runs at most one scrypt at a time.
`POST /api/login` answers `400` when `user` or `password` is empty (`401` is
reserved for a wrong password). An unauthenticated `GET /` redirects to
`/login`; API calls get
`401 {"error":"нужна авторизация","login":true}`.

Proxy auth accepts a request when the user header (`Remote-User` by default)
is non-empty and `x-mcp-proxy-secret` matches `MCPRIG_PROXY_SECRET` in constant
time. Both modes can be combined; either one is enough to pass.

Writes (`POST`/`PUT`/`DELETE`) are rejected when `Sec-Fetch-Site` is
`cross-site`, when `Origin` does not match `Host`, or when the body is not
JSON. A cookie-authenticated write must also carry `Origin`: without it the
request gets `403` "нужен заголовок Origin" — a browser sends it on every such
request, a forged one from another site does not. Header-based proxy auth and
login (`POST /api/login`, no cookie yet) are exempt.

`Origin` is compared in full — scheme, host and port; `MCPRIG_ALLOWED_HOSTS` is
not a list of sources (it names the panel itself). Behind a TLS proxy send
`X-Forwarded-Proto: https` or `X-Forwarded-Host` (plus `X-Forwarded-Port` when
the public port differs): otherwise the request looks like plain http and the
panel rejects an `https` Origin on writes. A configured `MCPRIG_PROXY_SECRET`
(trusted proxy) has the same effect; an `http` Origin is still rejected then.

`Host` must be in `MCPRIG_ALLOWED_HOSTS`; an empty list means loopback only.
