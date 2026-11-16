# Changelog

Notable changes to mcprig, newest first. The format follows
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/), versions follow
[SemVer](https://semver.org/). A release is cut from a git tag; GitHub
(`akrhin/mcprig`) is the public reference and carries the same archives as the
repository the project is built in.

## [Unreleased]

### Added

- The panel has pages of its own instead of one long **Overview**: `#/sets`,
  `#/servers` and `#/vars` are pills in the content head, `#/stats` and
  `#/set/<id>` keep working as before, and every page has an address that
  survives a reload and can be sent to someone else.
- **All servers** is a page of the whole host registry: name with the reason of
  a failure, transport, a health badge in colour (`healthy` green, `failed` red,
  `not checked` neutral — the same badge the set table uses), the tools a check
  found, the sets the server belongs to, and per-row **check**, **log**,
  **edit** and **×** that deletes it from the registry after naming the sets it
  leaves. The details drawer no longer carries the registry: it keeps the log
  tail of the set, so a set's own composition and a host-wide list stop sharing
  one screen.
- **Variables** is a page: the names your servers reference as `${NAME}`, each
  with whether it is set and where it comes from (`openbao`, `file`, `env`), a
  form that writes a value — name, value, store `automatic`/`OpenBao`/`file` —
  and **×** that forgets it everywhere. The panel says plainly that it never
  reads a value back and that a name no server mentions stays out of the list.
- The panel composes a set in place: the servers table of the **Overview** tab
  gains a registry picker with **add to set** and a **×** that detaches a server.
  Both send `PUT /api/sets/<id>` with the whole `servers` list and redraw from
  `GET /api/state`; a refusal shows under the block, an empty set says it will
  serve no tools, and the hint states that a running set takes the change by
  reload, not by restart.
- **Prompt** in the head of the set opens a dialog over the endpoint below: the
  task is required, the introduction and the server subset are optional, and the
  built text comes back monospaced with **copy** and **download .md**; `notes`
  are shown above it, and a stopped set warns beforehand that schemas will be
  missing.
- The keys block of a set shows the `keyMark` of `GET /api/state` as
  `key:<mark>` next to the prefix, with the statistics mark as its tooltip;
  hidden for an open set.
- `POST /api/sets/<id>/prompt` builds one text for an agent out of three
  parts: `{"task":"…","intro":"…","servers":["…"]}` and the answer
  `{"prompt":"…","notes":["…"]}`. The tool list comes from the live catalog of
  the set — names, descriptions and one-line input schemas — and a stopped set
  falls back to the `tools.lock.json` names with a note that schemas are
  missing. One schema is capped at 4000 characters, the whole prompt at 64 KiB,
  both cuts reported in `notes`; set tokens and variable values never reach the
  text.
- `GET /api/state` gives every set a `keyMark`: the `key:<6 hex>` fingerprint
  the statistics print for the set's primary key, so a row in the statistics can
  be matched to the key in the panel. Empty for an open set; `keyPrefix` stays
  as it was.
- A set may be created and started with no servers. An empty `servers` list is
  now a legal state: `POST /api/sets` and `PUT /api/sets/<id>` accept it, in an
  update a missing `servers` key leaves the list untouched, and a set without
  servers starts and answers the meta-tools (`list_servers` is empty). Deleting
  the last server of a set no longer answers `400` — the set keeps its key and
  port with an empty list.

### Changed

- A panel started without a password (`MCPRIG_AUTH=off`) now says so at
  startup: `WARN панель без пароля (MCPRIG_AUTH=off): доступна всем, кто
  дотянется до порта; задайте MCPRIG_PASSWORD`. `mcprig doctor` prints the same
  as `внимание:` under services when no `MCPRIG_PASSWORD` or
  `MCPRIG_PASSWORD_HASH` is set — a warning, not a problem, so the exit code
  stays `0`.
- `list_servers` no longer answers with `pid`: the proxy's process id is of no
  use to a client. `version` stays.
- `GET /api/calls` answers for a stopped set as well: its tail and counters come
  from the raw journal on disk (`sets/<id>/stats/raw-*.jsonl`) over the same
  7-day window as `period=7d`, up to the 512 newest rows, and a missing
  directory answers empty instead of landing in `unreachable`. `schema`,
  `throttled` and `unreachable` stay properties of a running set; a set without
  a token is anonymized on read.
- Signing a key (`PATCH /api/sets/<id>/keys/<keyID>`) reaches the set by reload
  instead of a restart: the label travels to the statistics client names and the
  set's pid stays put.
- Stopping the panel closes every set in parallel under one shared budget (15 s
  for readiness and 5 s for termination in total) instead of one budget per set,
  so a dozen sets no longer hold the exit for a minute.
- A refused start or update now answers by the kind of refusal instead of its
  wording: a busy port, a set that did not come up and a set without an active
  key answer `409`, and anything unclassified answers `500` with a line in the
  log and a generic message in the body. A set with an unfilled `${VAR}` in its
  environment is refused at start with `409` and the name of the variable
  instead of starting without it.
- `mcprig doctor` reads an empty install as healthy: a missing config and a
  missing registry together mean the setup has not been made yet, so it prints
  one line, exits `0` and no longer reports "3 problems" on a fresh directory.
- `describe_commands` answers within the caller's profile: the same allow and
  deny rules that hide a tool from `list_commands` now hide it here as well.
- `invoke_command` takes the server's slot: a slow upstream no longer lets an
  authenticated client stack calls past the per-server concurrency limit.

### Fixed

- `POST /api/browse` rejects a server entry carrying a reserved `${VAR}` or a
  `{file:…}` reference, like every other route that takes a server from the
  request body: the entry bypassed the registry, so the browse button could
  start a server the set would have refused.
- A stdio server now runs in its own process group and its whole group is shut
  down when the session closes: SIGTERM, a short wait, then SIGKILL. A server
  that wraps its work (`sh -c "npx … & wait"`) left its children behind — the
  process was re-parented to init and survived a probe or a set restart, where
  only the direct child was signalled. A probe that timed out on `initialize`
  no longer waits out the transport's five-second exit window either: the group
  gets the signal at once, so the answer stays inside the probe budget.
- A deny rule matches a tool name that spans a newline: the rule pattern is
  compiled with `.` covering every character, so `rm*` refuses `rm\n-rf`
  instead of letting it through.
- The quarantined-tool file is read and written with its errors reported: a
  lock file that cannot be read or written refuses `invoke_command` and
  `invoke_many` and logs the reason, instead of quietly turning the quarantine
  off.
- A probe error printed by `list_servers` has secrets scrubbed like every other
  place that shows upstream text.
- A webhook event that could not be delivered is no longer marked as sent: the
  next event of the same transition is sent again instead of being suppressed.
- `GET /api/sets/<id>/servers` no longer answers `200` with `servers: null` when
  the probe gave no answer: every server of the set stays in the list with the
  probe error, so the panel does not read an expired probe as an empty set. A
  client that cancels the request gets no answer; a start or reload cancelled by
  the client stops the spawned set process instead of leaving it holding the
  port.

## [0.5.1] - 2026-11-15

### Fixed

- `TestEnvPutRestartsOnlySetsUsingVar` stops the restarted set and waits for it
  to exit before the temp directory is removed; the race used to fail the
  verify job on CI.
- Builds move to Go 1.27.4 and `golang.org/x/net` v0.62.0: govulncheck reports
  no reachable vulnerabilities (GO-2026-6859 and the x/net advisories).

## [0.5.0] - 2026-11-15

### Added

- Downstream resources and prompts reach the MCP client: a server whose config
  names them in `expose` (`["resources"]`, `["prompts"]`, or both) contributes
  them through the new `list_resources`, `read_resource`, `list_prompts` and
  `get_prompt` tools. Without `expose` the client still sees tools alone, and an
  unknown value is a config error — the server does not start. Permissions
  address them as `resource:<uri>` and `prompt:<name>`, so the existing rules
  and profiles apply unchanged; answers are cut at `maxBytes` (32768 by default
  for a read) and the full one lands in `tool-out`. The listings honour the same
  rules: a `default: deny` keeps a resource out of `list_resources` until a rule
  allows it.
- `expose` is editable from the panel: the server form carries two checkboxes
  (resources, prompts), the registry reads and validates the field on `POST
  /api/servers` and `PUT /api/servers/<name>`, and an unknown value is a `400`
  that names what is allowed. Unchecking both removes the field.
- An edit no longer restarts a whole set: `PUT /api/sets/<id>`, `PUT
  /api/servers/<name>` and the new `POST /api/sets/<id>/reload` write
  `servers.json` and send `SIGHUP` to the set process alone — its stdio children
  have no handler and keep running. The process rereads the file and answers in
  `reload.status` next to it (`nonce`, `generation`, `applied`, `restartNeeded`,
  `error`); the nonce the panel puts into `reload.request` before the signal
  keeps a late answer to an earlier attempt from being taken for this one. Calls
  in flight to the other servers keep their sessions, and the set's pid and
  restart counter stay put. A transport change (`port`, `open`, `path`,
  `host`, `allowedHosts`) is still applied by a restart and lands in
  `restartNeeded`; keys are the exception and travel by reload (below); `path`, `host` and `allowedHosts` in a hand-edited
  `servers.json` land there too and the set is restarted by hand. A server's
  `env` and `envAllow` are not reloadable either, so an edit of them restarts
  the sets using that server. A broken config leaves the process on the previous one and
  reports the cause. A failed reload does not lose the edit: `PUT /api/sets/<id>`
  answers 200 with `reloadError`, and `PUT /api/servers/<name>` lists the sets it
  could not apply in `reloadErrors`. That answer now names the sets `reloaded`
  instead of `restarted`; `restarted` still carries the same list for one
  release.

- A set serves several keys at once. Every key carries `id`, its token,
  `createdAt`, `revokedAt`, `profile` and `label`; the token never leaves the
  server and the hash lives only in the listener's memory. Issuing a key no
  longer revokes the previous one, and revoking one key no longer stops the
  set — only the last active key does. A set keeps up to 8 active keys and the
  ninth answers `409`. The routes are `GET /api/sets/<id>/keys` (ids, labels,
  profiles and dates, never a token), `POST /api/sets/<id>/keys` with an optional
  `{"label":…,"profile":…}` (the token is in that answer only) and `DELETE
  /api/sets/<id>/keys/<keyID>`; `PUT /api/sets/<id>` with `token` still replaces
  the primary key, and creates one when the set has no active key. A new key
  reaches a running set by reload, not by restart: the transport takes the whole
  `authTokens` list, compares the presented token with every active key's
  sha256 in constant time per element, and takes the caller's fingerprint — and
  so its profile and rate limit — from the token that matched. Keys issued
  before this release read as a one-element list, and a `note` becomes the key's
  `label`.
- The panel states where it is reachable: `MCPRIG_PUBLIC_URL` sets the public
  address (`http(s)://host[:port][/prefix]`), and it becomes the only source of
  the OAuth redirect (`<publicUrl>/oauth/callback`, path prefix kept) and of the
  decision that the session cookie is `Secure`. A bad value — no scheme, no
  host, credentials, query, fragment or a
  trailing `/` — stops the panel at startup instead of falling back silently.
  Without it the redirect is built from `MCPRIG_HOST` and `MCPRIG_PORT` as
  before. `mcprig doctor` prints the effective redirect and flags one that
  cannot receive the code (no host, port `0`, loopback behind `https`).
- The panel can serve TLS itself: `MCPRIG_TLS_CERT` and `MCPRIG_TLS_KEY` make it
  listen over https. Both are required together, and an unreadable file fails
  startup loudly instead of quietly dropping to http.
- A set can cap how often a caller calls its tools: `rateLimit:
  {"perMinute": 120, "burst": 20, "perTool": {"serena/find_symbol":
  {"perMinute": 10, "burst": 2}}}` in the set's record. Each inner call of
  `invoke_many` counts on its own, a caller is identified by the fingerprint of
  its key (in an open set, by its address), and a refused call is an ordinary tool error that never reaches a
  server — `/stats` reports it as `throttled`. No block, or zeros, means no
  limit. The field is edited in `registry.json` or over the API, as `notify`.
- A truncated tool answer can be fetched over HTTP: `GET /tool-out/<name>`
  serves the saved file behind the same guard as `/mcp` — bearer key,
  `allowedHosts`, Origin — and the truncation marker names that URL when the set
  runs over HTTP with a key. The file is never served anonymously, even in an
  open set, and a name past `MCPRIG_TOOLOUT_TTL` (24h by default) answers `410`.
- `mcprig status` shows panel health and, per set, its state, port, tool count,
  key and last call; `mcprig logs` prints a set's or a server's log tail with
  `-f` and `--json`. Both take `--panel` (default `MCPRIG_PANEL_URL`, else
  `MCPRIG_PORT`) and log in with `MCPRIG_PASSWORD` or the reverse-proxy secret.
- A set can report failures on its own: `notify: {"webhook": "https://…"}` in the
  set's record sends `set.crashed`, `set.restart_failed` and
  `server.unhealthy` as one JSON POST per transition — 5 s timeout, three
  attempts, and never a token, `env` or command in the body. The address itself
  never reaches the log, even when it carries a query token.
- The panel has a **Log** drawer per server and **Show more** under the call
  history: the drawer reads the set's log filtered by server name, and the
  button pages `/api/calls` by `before` until a page comes back short. Paging
  pauses the 5 s refresh and shows **resume** next to it, which returns to the
  first page.
- A key can be narrowed to a profile of its set: `profiles: {"reader":
  {"deny": ["*/*_write"], "allow": ["*"]}}` in the set's record, and a key
  issued with `{"profile": "reader"}` (a `400` lists the set's profiles, or
  says the set has none). The profile applies after `permissions` and only
  narrows — a rule's denial keeps its own message. `list_commands` hides what
  the profile refuses, `invoke_command` and `invoke_many` refuse it as a tool
  error naming the profile, and a key without a profile behaves as before.
  The field is edited in `registry.json` or over the API, as `notify`.
- Tracing crosses the proxy: a client's W3C `traceparent` in `_meta` (SEP-414)
  is validated (version `00`, non-zero ids, no `ff`), becomes the parent of the
  call's span and goes down to the upstream in the `_meta` of `invoke_command`,
  `read_resource` and `get_prompt`; nothing else from the client's `_meta` is
  forwarded. Spans — `mcp.tool.call`, `mcp.server.connect`, `mcp.reload`, with
  names and outcome only, never a key or its fingerprint — are exported over
  OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, and cost nothing (no
  recording, no network) when it is not.

- A changed tool descriptor is quarantined: the set remembers `name`,
  `description`, `inputSchema`, `outputSchema` and `annotations` hashed into
  `sets/<id>/tools.lock.json`, approves a tool the first time it appears
  (`approvedBy: "auto"`) and refuses `invoke_command` and `invoke_many` for that
  tool once the descriptor differs — `isError`, `_meta.lazyMcpPolicy:
  "quarantine"` and a message with the old and new hash and the command that
  lifts the block. Neighbours on the same server and other calls of the same
  batch run as usual; `list_commands` marks the tool `changed: true`.
  A person approves the new descriptor from the **Tools** drawer of the panel
  (`GET /api/sets/<id>/tools`, `POST /api/sets/<id>/tools/approve`, `{"all":
  true}` included) or with the new `mcprig approve --set <id> <server/tool>…`/`--all`,
  which talks to the same routes. The check does not depend on a warm
  catalogue: a cold cache is filled first, and a catalogue that cannot be read
  refuses the call (`list_commands {refresh:true}` no longer empties the
  neighbours' caches either). The panel's **browse** session applies the same
  record — a changed tool is marked in its listing and refused with `403`
  before the downstream sees the call. The record survives a set restart, and
  entries of a server removed from a set are pruned when the set config is
  rebuilt. Without a set data directory (`MCPRIG_DATA_DIR`) there is no record
  and no quarantine.

### Changed

- `X-Forwarded-Proto` and `X-Forwarded-Host` are trusted only when
  `MCPRIG_PROXY_SECRET` is set. Without it both are ignored for the Host and
  Origin checks and for the `https` mark, so a client can no longer claim
  `https` or a different public host by sending a header; the OAuth redirect
  never depends on them. The session cookie is `Secure`
  whenever the public address is `https` or the panel has its own certificate,
  no longer only when the request itself arrived over TLS.
- Login failures are counted by the socket address *and* by the address from
  `X-Forwarded-For`: rotating the header behind a trusted proxy no longer
  bypasses the five-per-minute limit, and a request is refused when either
  counter is over it.
- Browse sessions in the panel now apply the server's `permissions` to tool
  calls, the same gate `invoke_command` uses: a denied call answers `403`
  instead of reaching the server. Resources and prompts are not covered yet.
- A server's `permissions` block now survives the panel and the registry:
  `POST /api/servers`, `PATCH /api/servers/<name>` and import keep it in
  `registry.json`, so browse and `invoke_command` enforce the same rules. A
  broken block is refused with `400` instead of being dropped.
- `_configError` no longer appears in a config file: a validation defect stays
  in memory (`list_servers` still reports it as `healthy: false`), and a file
  that already carries the key stops marking that server broken on the next
  load.
- Russian user-facing text is proofread with `ru-text`: panel strings, CLI
  output and server error messages use sentence case, the «вы» form, one term
  per concept and the "what — why — what to do" shape of an error. Counts agree
  with their numeral, percentages print as `4,8%`, `mcprig auth` names the
  deadline as a clock time, and the schema line shows the tool count it already
  passed in. The Russian documentation follows the same rules.
- The panel's **Overview** tab now leads with the set's servers: the seven-day
  summary (calls, error rate, p50, p95) moved below the servers table and reads
  as a one-line caption, the set rail ends with `N sets · K running`, and the
  action column has an `Actions` header. **Copy key** is disabled while the set
  is open and has no key prefix, it copies the bare prefix rather than the
  `prefix…` shown in the panel, and the unused **Reveal key** button is gone.
- Documentation is restructured: `docs/en/index.md` and `docs/ru/index.md` are
  the entry points, every product page carries a language switch and a "See
  also" block, and plans, decisions, audits, proposals and the TS contract moved
  to `docs/history/`. `bash scripts/docs-check.sh` (also a CI step) fails on a
  broken link, a personal path, an orphan page, language drift and a stale fact
  in `docs/anchors.txt`.

### Added

- The **authorize** button in a server row of the panel runs the OAuth flow from
  the browser: the link is drawn in a dialog, the code comes back to
  `http://<panel host>:<panel port>/oauth/callback`, and the set restarts itself
  once the token is saved. A provider that refuses plain `http` for a
  non-loopback address (dynamic registration answers `invalid_redirect_uri`)
  still needs TLS in front of the panel.
- `mcprig auth --set <set> <server>` authorizes a server that asks for OAuth:
  the command prints the authorization link and waits for the redirect instead
  of starting a browser, so it works over SSH and on a host without a graphical
  session. The token goes to the set's `oauth.json` and is picked up on the next
  set start. The interactive step deliberately lives outside a client request:
  a set's health probe allows 15 s and a person at a browser needs minutes.
- The **Calls** tab lists the meta-tools `list_servers`, `list_commands` and
  `describe_commands` (server `mcprig`) and the reason a call failed: the text
  is shortened in the row and shown in full in a tooltip. Previously only calls
  of child tools reached the history and the counters.
- The raw history journal (`raw-YYYY-MM-DD.jsonl`) carries an `err` field: the
  failure reason as one line of at most 300 bytes with newlines replaced by
  spaces. Old rows without the field are read as before.

## [0.4.0] - 2026-11-11

### Added

- The panel speaks English and Russian: English by default, an `EN`/`RU` switch
  in the header, the choice kept in `localStorage['mcprig.lang']`, `<html lang>`
  following it, numbers and dates going through `Intl`. Every user-facing string
  lives in one `DICT` in `web/app.js`.
- A set client is named by its key note in statistics: `PATCH
  /api/sets/<id>/keys/<keyID>` with `{"note":"hermes"}` writes the note, the set
  gets it in `transport.clientNames` under the key fingerprint (6 hex of sha256,
  no token) and counts the client as `hermes` instead of
  `key:ce80c4/python-httpx/0.28.1`. An empty string clears the note and restarts
  a running set; the set documentation gained `clientNames`, and `/api/sets`
  gained the key `note`, which the record already had.

### Changed

- The panel no longer waits for sets to restart after `PUT`/`DELETE
  /api/env/<NAME>`: the answer comes at once, sets using that variable restart
  in the background with a 60 s budget, errors go to the log, and `restarted`
  still lists the ids.
- `/api/stats` caches parsed history files: a file is re-read only when its
  `mtime` or size changes, so a repeated period request does not touch the disk.
- `PUT /api/sets/<id>` checks the active key and a free port **before** writing
  to the registry: a rejected edit no longer leaves a running set stopped.
- `DELETE /api/sets/<id>/keys/<keyID>` reports the stop of an active key with
  `stopped: true` and one log line.
- Images are served from GitHub Container Registry — `ghcr.io/akrhin/mcprig`
  (no login needed); the Gitea registry is closed and is not a public channel.
  The release GitHub workflow builds and pushes the tags itself.
- The panel moved to a redesign: the set rail shows only a set's identity and
  state, the header dropped its counters, the KPI strip counts the last 7 days
  of history, statistics gained a filter bar with labels above the selects, and
  the right column became a details drawer with the registry, the variables and
  the log. Tabs are **Overview**, **Calls**, **Statistics**.
- Four themes instead of six: dark (default), light, Nord and Gruvbox on one set
  of CSS variables. Old names in `localStorage['mcprig.theme']` keep working and
  are rewritten (`mcprig`,`tokyo` → dark, `solarized` → light).
- The **Stop** button is back in the set header, separate from **Restart**.
- The **Statistics** tab shows the set selected in the rail and follows its
  switches; its own set list no longer drifts from the rail.
- The set page shows the set's own server table (name, type, health, tools, last
  call) instead of the table of all sets; the second line of a server row
  carries the daemon error, the isolation or the description.

### Fixed

- An OAuth receiver started with a `redirectUrl` that has no port (or has
  `:0`) is rejected: the browser has nowhere to return, and the old ephemeral
  port meant five minutes of silent waiting.
- The `bwrap` probe is capped at 10 seconds and shared between parallel starts:
  a hung `bwrap` no longer blocks sandbox servers forever, and a failure is not
  cached — the next start tries again.
- A background set restart after a variable or server change is cancelled with
  the panel: the goroutine used to outlive `Close` and leave a child process
  alive on the set port.
- Server import and add reject `{file:…}` anywhere in a value, not only at the
  start: `p{file:/…}` no longer smuggles a file into the set environment.
- Deleting a set's last server answers `400` "server X is the last in set Y",
  and sets are not stopped: the registry is written first, the stop follows a
  success.
- A race in `startLocked`: the process fields (`exited`, `stopping`,
  `restarts`) are read under a single `rt.mu` lock, matching how `wait` and
  `kill` write them.
- Server browsing no longer fails with "session closed" on a long call:
  `lookup` holds the session by refcount, `close`/`sweep` close it after
  `release`.
- Expired panel sessions are removed by the health ticker, not only by `get`.
- A failed log rotation loses no output: the old file stays and
  the error is visible in the set status `error` field.
- The log tail is read from the last kilobytes through `Seek` instead of the
  whole 5 MiB file on every panel poll.
- A set's `servers.json` is written through `internal/atomicfile` — with a
  directory fsync and `0600` permissions.
- A set's `/stats` request passes its Host guard: the panel sets `Host` from
  the first `AllowedHosts` entry.
- A policy refusal inside `invoke_many` carries the same marker in `result` —
  `🚫 Blocked by mcprig policy: …` and `_meta.lazyMcpPolicy: "blocked"` — as
  `invoke_command`: a hook client sees the refusal in a fan-out, not just an
  `error` string.
- Cutting an answer with `structuredContent` adds the line
  `[structuredContent does not fit maxBytes: full answer in the file]` to the
  marker — it is visible that the structure was dropped and lies in the dumped
  file.
- With both names empty, `invoke_command` and `invoke_many` say
  `specify command_name or command` instead of `command_name is required`.
- `list_servers`: a probe that did not get a free slot no longer marks the
  server `healthy: false` for 15 seconds — the outcome is unknown, and the
  answer is `healthy: true, unknown: true` with no `error` and no cache write.
  The panel shows "not checked" for such a server instead of "health ok".
- The panel got `IdleTimeout` 60 s; `WriteTimeout` is not set — long response
  streams must not be cut.
- In the panel: statistics tables no longer give ragged heights in two columns,
  the "Requests" label no longer runs into the filters, a long key in the header
  is truncated, and the theme menu on a phone became a bottom sheet that does
  not cover the header buttons; Y-axis labels at small numbers show fractions
  (0.25) instead of three identical "1"s.
- The details drawer on a phone no longer stretches registry rows wider than the
  screen.
- The key note is edited in the **Keys** dialog: a row has a note field and a
  **Save** button that appears only when the value differs from the saved one; a
  set's `restartError` is shown as a separate warning.
- The `slim`/`full` images are built and published on the tag by the workflow
  itself: `X.Y.Z-slim|full` and `latest`/`latest-full`; without the `REGISTRY*`
  secrets the step is skipped. The registry used to be filled by hand while the
  docs promised automation.
- `Dockerfile`: `VERSION` must have no leading `v` (the `vX.Y.Z` form is
  rejected by the build), `org.opencontainers.image.*` labels appeared, `uv` is
  pinned, and `HEALTHCHECK` probes both 9100 and 9091 — the panel container no
  longer hangs `unhealthy`.
- `MCPRIG_*`, `BAO_*` and `OPENBAO_*` in `envAllow`/`env` and in `${…}` inside
  `headers` are rejected at set load: the promise that reserved names do not go
  down became a check rather than advice.
- `{file:…}` is caught anywhere in a string (`config.HasFileRef` on the same
  template as the expander): `p{file:/…/env.json}` no longer drags a file into
  a child's environment.
- `transport.authToken` goes through the reserved-name check like `env` and
  `headers`: `${MCPRIG_…}` in the token is no longer expanded.
- A broken server does not sink the whole set: the `json.Unmarshal` error stays
  in that record's `_configError` and the other servers of the set still load.
- `DELETE /api/servers/<name>` refuses when the server is the last in the set:
  an empty set is no longer created. The answer is `400` with
  `server X is the last in set Y`.
- `doctor` reads the panel password from `panel.env`, then from the legacy
  `env`; quotes are stripped in pairs, `export` and comments are understood.
- A server connection is limited to 30 seconds, and `Close` reports that the
  connection did not finish instead of staying silent: a hung `initialize` no
  longer holds a call until the client times out.
- A retry over the network does not fire on a timeout: a `net.Error` whose
  `Timeout()` is true is no longer a reason to repeat, so there is no double
  side effect.
- The per-server concurrency limit is shared by all requests instead of being
  computed afresh inside every `invoke_many`; the limit key is the server
  record, so invented names do not multiply slots.
- `list_commands` and `describe_commands` accept `maxBytes` (up to 1 MiB);
  without the field the catalog comes whole, as before. The dumped file holds
  the whole answer (the old 4 MiB ceiling is gone), and the text marker names
  the path.
- A key note is not overwritten by the client label: `key:<fp>/<clientInfo>`
  shows as the note when one is set.
- The `bwrap` probe caches only success: a single failure at host load no longer
  forbids the sandbox until the process restarts.
- OAuth checks `state` before handling `?error=`: a foreign callback does not
  clear the pending wait, and the receiver address comes from `redirect_uri`
  rather than a separate `callbackPort` — the silent five-minute timeout is
  gone.
- `Bearer` with extra spaces yields both a label and access, not a label without
  access.
- The panel polls `/api/stats` only on the **Statistics** tab; after revoking a
  key that stopped a set, a warning is shown.
- The `mcprig-set@` unit: `EnvironmentFile` is optional — a set without `env`
  starts instead of looping on restarts.

### Internal

- The `init --client opencode` samples gained the `"servers"` level, as the
  binary prints it; `deploy/mcprig-warm.service` calls `9091` (the panel
  default, not one host's port); `tool-out` cleanup runs every ten minutes
  instead of hourly; `doctor` has five sections; the README says two scrypt at
  once and five meta-tools; `docs/history/decisions/0003-naming-and-repo.md` is marked superseded; the
  Russian install caught up with the English one on `MCPRIG_HOST` and rootless
  Docker.

## [0.3.8] - 2026-11-10

### Added

- `invoke_command` and `invoke_many` accept `command` as a synonym of
  `command_name` — that is how `list_commands` names the field; both keys with
  different values are rejected.
- `CONTRIBUTING.md` and `CONTRIBUTING.ru.md`: where an issue and a pull request
  go, the commit form, the gate before pushing, and the rule that a behaviour
  change updates both doc trees and the changelog.
- `.gitea` issue forms for a bug and a proposal, plus a pull-request template
  that asks what changed, the gate result and the docs touched.
- Versioning rules in both contributing files: SemVer pre-1.0, the tag is the
  only version source, what a `PATCH` and a `MINOR` may carry, and how a release
  commit and a published tag are treated.

### Fixed

- Tool schemas no longer declare `command_name` required: it and `command` are
  equal names (JSON Schema `anyOf`), so a client that checks `required` accepts
  `command`.
- `invoke_many` moved to the common error style: a value type error reads
  `key command: expected type "string", got type "number"` with the call index,
  an extra key lists the allowed ones, and the hint mentions the synonym.
- Top-level arguments that are not an object become
  `arguments: expected type "object", got type "string"`, with no Go type name.
- The extra-keys error lists the allowed keys, and the type error names the key
  and the kind of value (`key server: expected type "string", got type
  "number"`) instead of a Go struct name.
- An isolation reason no longer doubles the prefix: `no isolation: bwrap not
  found in PATH` instead of `no isolation: sandbox unavailable: …`.
- `doctor` checks `prlimit` and prints warnings as `warning:`, not `ok:`.
- Documentation: `command` in the `invoke_command` table (en/ru), image build
  with `--build-arg VERSION=` (otherwise the binary answers `dev`).
- Images carry `bubblewrap` and `prlimit` (`util-linux`): `"sandbox": {}` no
  longer fails with `bwrap not found in PATH`, and the sandbox no longer rejects
  `prlimit` as a relative path.
- The `full` image carries uv/uvx in `/usr/bin`: stdio servers through `uvx`
  start.
- Both images set `MCPRIG_HOST=0.0.0.0`: a panel in a container answers on the
  published port, not only on loopback inside.
- A server error no longer promises `bwrap` to a server that did not get the
  sandbox: `list_servers` shows the real isolation.

## [0.3.7] - 2026-11-10

### Added

- Panel shows each server's isolation: a muted `bwrap` badge when the sandbox
  applied, a warning `no isolation` badge when bubblewrap did not start, and
  `config rejected` for a record with `_configError`.

## [0.3.6] - 2026-11-10

### Added

- The panel edits the host server registry: `GET/POST /api/servers`,
  `PUT/DELETE /api/servers/<name>` and a form in the drawer with transport
  fields, `env`/`envAllow`, tags and the `sandbox` block (`mode`, `onUnavailable`,
  `ro`, `rw`, `network`, `memoryMb`, `pids`, `tmpMb`, `timeoutSec`). A backend
  refusal is shown in the form and the record stays unchanged.
- The panel mock computes statistics by filters and fills the counters of all
  sets from `bySet`.
- `mcprig stdio` keeps a call history: `$MCPRIG_DATA_DIR/stdio/stats/` holds the
  same `raw-*.jsonl`, `day-*.json` and `month-*.json` as a set directory, and
  period slices are read from disk. The client in the row is `stdio`: the
  transport is the only client of the process. An unreachable directory is a
  warning on stderr and the proxy keeps serving.
- `GET /api/stats?set=all` (and an empty `set`) returns `bySet`:
  `{set, calls, failures, totalMs}` per set with calls in the period — the set
  table shows counters without opening a set. A set with no calls in the period
  is not in the slice and answers no zeros; a single set has no `bySet`.

### Changed

- `period=7d` keeps a latency distribution in memory (ms value → call count)
  instead of the list of all calls: memory depends on the number of distinct
  latency values, not on the number of journal rows. Percentiles stay exact
  (`approximate:false`).
- Set browsing shows `logging` by the presence of the `logging/setLevel` tool in
  `tools/list` rather than `ServerCapabilities.Logging` (deprecated since
  protocol revision 2026-07-28, SEP-2577). Where the tool exists, the look and
  behaviour are unchanged.

### Fixed

- `prlimit` with a dynamic loader is mounted into the sandbox by itself (like
  `/lib`, `/lib64`, `/usr/lib`, `/usr/lib64` when present), so an explicit `ro`
  without `/usr` no longer breaks the start of a server with limits.
- `mcprig stdio` cleans `tool-out` (`MCPRIG_TOOLOUT_TTL`/`MCPRIG_TOOLOUT_MAX`)
  with the same sweep at start and hourly as `serve` and `panel`, in the same
  base directory: stdio process dumps no longer stay forever.

## [0.3.5] - 2026-11-10

### Added

- On-disk call history per set (`sets/<id>/stats`, and next to the config for a
  standalone `serve`): `raw-YYYY-MM-DD.jsonl` one line per call,
  `day-YYYY-MM-DD.json` with daily aggregation and a fixed histogram
  (1…10000 ms, +∞), `month-YYYY-MM.json` for months older than 30 days.
  Retention: raw — today + 7 days, day — up to 30, after that only the month;
  rotation and cleanup at set start and once a day. Writes go through a bounded
  channel and one goroutine: a tool call does not wait for the disk, a full
  channel drops the row and counts it in the set snapshot's `stats.dropped`.
  Files `0600`, directory `0700`.
- `GET /api/stats?set=<id|all>&period=today|7d|30d|archive&server=&tool=&client=&bucket=hour|day|month`:
  totals, a series over time and breakdowns by tool, server and client. For ≤7
  days percentiles are computed from the raw journal exactly
  (`approximate:false`), for 30 days and the archive from the histogram
  (`approximate:true`, bucket midpoint). `set=all` sums all sets; a set without
  a history directory lands in `unreachable`. An unknown `bucket` is `400`.

### Changed

- The set log is now `sets/<id>/mcprig.log` (was `lazy-mcp.log`). On a set's
  first start the inherited `lazy-mcp.log` is renamed and the tail is not lost;
  the `servers.json` format is unchanged.
- The panel writes set start and stop and a server going unhealthy to its own
  stdout (systemd collects it): `set <id>: started|stopped`,
  `set <id>: server <name> unhealthy` with `reason`. Unhealthy is logged only on
  a state change, not on every health poll; the state is polled in the
  background every 30 s, so the lines appear without an open panel.
- At start the panel folds `day-*.json` into monthly files for all sets: a set
  stopped for good no longer loses its days from the archive. The archive also
  reads unfolded `day-*.json` older than 30 days.
- The `stopped` line is written by the common `Stop`/`Restart` path: a restart
  no longer skips it, and `Stop` after a process crash no longer invents it.

### Fixed

- A request with a session cookie but no `Origin` no longer passes: with cookie
  auth `POST`/`PUT`/`DELETE` require `Origin` to match the request in full —
  scheme, host and port (`403` "Origin header required").
  `MCPRIG_ALLOWED_HOSTS` is no longer an Origin source: a panel on another port
  of the same host cannot stop a set. Behind a TLS proxy the original scheme and
  host come from `X-Forwarded-Proto`/`X-Forwarded-Host`, and
  `MCPRIG_PROXY_SECRET` is trusted as a proxy even without them (`403` "origin
  does not match host" stays for a foreign origin). `POST /api/login` is
  unaffected.
- An unknown `command_name` is now rejected from the server's tool cache before
  calling upstream, with one text — `unknown tool "x" on server "y"`; the
  previous three variants (`unknown tool %q`, `rejected by transport`, an
  upstream answer) no longer diverge. An empty cache — the call goes as before.
- A server rejected at load no longer promises `isolation: bwrap` in
  `list_servers` and health: the field answers `no: config rejected`.
- `prlimit`, through which the sandbox sets limits, is mounted together with its
  loader and libc: an explicit `ro` without `/usr` no longer gives
  `bwrap: execvp /usr/bin/prlimit: No such file or directory`.
- A broken `day-*.json` no longer blocks the month fold and raw cleanup: the
  file is skipped with a warning in the set log. A rename failure
  `lazy-mcp.log` → `mcprig.log` is also logged as a warning instead of
  swallowed.
- The sandbox probe went through `/bin/true` (a symlink): with `/bin` ahead in
  `PATH` the probe run failed "exec inside the sandbox failed", and with
  `onUnavailable: deny` no isolated server started. The path is now resolved
  through the symlink and `/bin` is bound in the probe.
- `prlimit` was run by name from the child's PATH: without `/usr` in `ro` it
  failed `bwrap: execvp prlimit: No such file or directory` with no hint. Now an
  absolute path, and `doctor` warns about `sandbox.ro` without `/usr`.
- The `sandbox.memoryMb` default is raised from 512 to 2048: under 512 not only
  node failed to start but any Go server (`failed to reserve page summary
  memory`).
- A typo in a `sandbox` field name (`memoryMB`, `cwd` inside the block) is no
  longer silent: `config.Load` and the panel answer `sandbox: unknown fields:
  …`.
- `sandbox.mode=none` with limits in the block now warns — the block used to
  look configured while the limits did not apply.

## [0.3.4] - 2026-11-10

### Added

- Sandbox for untrusted stdio servers: `sandbox` block on a server
  (`mode: bwrap`, `onUnavailable: deny` by default), running the child under
  bubblewrap — own mount tree, read-only root, no `/home`, no network, no
  `/proc/1/root` — with `prlimit` limits inside the namespace for address space
  and process count. Fail-closed: if bubblewrap cannot start, the server does
  not start either. `scripts/sandbox-e2e.sh` checks all of it against the host.
- Servers tagged `untrusted` require a sandbox; without one the config is
  rejected and the server never runs.
- `envAllow` per server, and a warning in `mcprig doctor` for `npx -y` / `uvx`
  without a pinned version, for an `envAllow` name the environment does not
  have, and for `memoryMb` below 2048 on a node command.
- `sandbox.tmpMb` (default 128): the `/tmp` tmpfs is size-limited and carries
  `RLIMIT_FSIZE`, so a child cannot fill the host disk. `mcprig doctor` probes
  bubblewrap itself.
- `list_commands` takes `namesOnly`: an array of tool names instead of the full
  catalog (arr-status: 89 KB and 443 tools), plus the document-everywhere
  `refresh` in its schema. A multi-part text answer is now joined with newlines
  instead of being wrapped in JSON, so the cut marker points at readable text.
- tool-out is swept every 10 minutes instead of hourly: a chatty server writes
  about 2 MiB an hour, and the old cadence let the 256 MiB cap overshoot.
- `list_servers` and the panel health answer carry `isolation`: `bwrap` when
  the sandbox is applied, `no isolation: <reason>` when it was requested but
  unavailable. The note clears once the sandbox works again.

### Fixed

- A stdio server started by `mcprig serve` or `mcprig stdio` received the whole
  environment of the host process, including every API key in it. Now it gets a
  fixed base (`PATH`, `HOME`, `LANG`, `TMPDIR`, `LC_*`, `XDG_*`, `SSL_*`,
  `TZ`, `NODE_EXTRA_CA_CERTS`) plus what `env` and `envAllow` name explicitly.
- Editing a server in the panel dropped `sandbox` and `envAllow`: the registry
  parser did not read them, so the API answered `200` and the isolation
  disappeared on the next save. Both round-trip through add, update, import and
  export now.
- `untrusted` accepted `onUnavailable: warn|allow`, which is the same
  unprotected start only quieter; only `deny` passes.
- The `probe` and `browse` buttons, and `mcprig warm`, started a server without
  the validation `config.Load` applies, and `warm` still handed the child the
  whole host environment.
- A sandbox that failed to apply was visible only in the package log; `warn`
  and `allow` made the server run unprotected while health said it was fine.
- When a child died before `initialize`, the reason in its stderr was lost:
  Go sends stderr to `/dev/null`, so `bwrap: execvp …: No such file` arrived as
  a bare `EOF`. The last 4 KB of stderr now accompanies the error, with the
  server's own secrets masked.
- A `timeoutSec` that fired left the session alive, so the next call could read
  the previous answer; the session is dropped and the error unwraps to
  `context.DeadlineExceeded`.
- `probe` hard-coded `/usr/bin/true` and ran on every connect; it now resolves
  `true` through `PATH` and caches a success.
- A missing `rw`/`cwd` path was silently skipped, leaving the server without the
  directory it was meant to have; only missing default `ro` paths are skipped.
- `Check` filled defaults in the shared registry object, racing with set
  building; it now validates a copy.

## [0.3.3] - 2026-11-10

### Added

- Panel: six colour schemes — mcprig dark (default), light, Nord, Gruvbox,
  Solarized, Tokyo Night. Chosen in the header, remembered in `localStorage`,
  the first visit follows `prefers-color-scheme`. Themes are plain classes and
  a separate `themes.css`, so the panel CSP is unchanged (no inline styles).
- `POST /api/sets` returns the issued set token once in the `201` body; lists
  and the registry view still never repeat it.

### Fixed

- Tool answers are scrubbed of server secrets before counters and `tool-out`:
  values of `env`/`headers` with key/token/secret/pass/auth/credential in the
  name are replaced with `***`, so upstream error texts no longer leak API keys.
- Panel health beats the set's own `healthTimeout`: the load budget is 30 s
  against 15 s per server, so a restarted set no longer shows
  `context deadline exceeded` for the whole set.
- `POST /api/login` answers `400` for empty fields instead of a misleading `401`.

## [0.3.2] - 2026-11-09

### Changed

- Releases are mirrored to GitHub: `.github/workflows/release.yml` builds the
  same archives (goreleaser `--skip=publish`) and uploads them with `gh`.
- The goreleaser install step in both workflows fails loudly when `checksums.txt`
  or the archive could not be downloaded, instead of dying inside `sha256sum`.

### Fixed

- The stdio handshake now uses `initialize` (protocol `2025-11-25`), not
  `server/discover`: Python MCP SDK ≤ 1.9 fails to parse the new method, stops
  reading, and the client waited out its 15 s budget — qbittorrent in the `ha`
  set showed `context deadline exceeded`. HTTP sets keep the modern handshake.

## [0.3.1] - 2026-11-09

### Added

- Docker images in the Gitea registry: `0.3.1-slim`, `0.3.1-full`, and
  `latest`/`latest-full` following the release. `--build-arg VERSION` bakes the
  version into the binary, so `/healthz` reports the release, not `dev`.

### Fixed

- `tool-out` dumps are cleaned by TTL (24 h) and a 256 MiB cap.
- `mcprig doctor` reads the panel's environment before reporting, so its port
  and secrets are seen from the panel side.

## [0.3.0] - 2026-11-09

### Added

- `invoke_many`: up to 8 independent calls in one request.
- `list_commands` can be filtered by prefix; `invoke_command` caps large answers
  (`maxBytes`) and writes the full text to `tool-out`.
- Live tool-list refresh and one safe retry per call.
- `mcprig doctor` (health report) and `mcprig warm` (cache warm-up) with a
  systemd timer.
- Panel: client label from request `_meta`, schema weight counted in tokens.

### Fixed

- `${VAR}` resolution is parallel (up to 8 at once); logins queue one scrypt at a
  time; a busy explicit port is rejected on set creation; `goreleaser check`
  runs before the release job publishes.

## [0.2.0] - 2026-11-09

### Added

- Secrets from an OpenBao-backed store with a 30 s cache; `DELETE /api/env`
  reports where the value came from.
- Hardening from the paladin/arbiter review: cross-host redirects with
  `Authorization` are refused, the login limiter counts the client address
  rather than a proxy-appended one, logs are capped at 5000 lines, `{file:…}`
  is rejected in env and headers, `store=local` accepted as an alias, a string
  `command` rejected on import, sets polled in parallel in `/api/calls`.

## [0.1.0] - 2026-11-09

### Added

- First release: one binary that puts a whole MCP set behind 5 meta-tools
  (`list_servers`, `list_commands`, `describe_commands`, `invoke_command`,
  `invoke_many`) instead of hundreds of tools in the prompt.
- Registry and web panel over the same sets: add servers, groups, keys, call
  tail, per-set logs, env and secrets.
- HTTP and stdio transports; `init` prints client configs.
- Docker images (slim and full), Gitea Actions release with goreleaser and
  govulncheck, Go 1.27.3.

[Unreleased]: https://github.com/akrhin/mcprig/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/akrhin/mcprig/compare/v0.3.8...v0.4.0
[0.3.8]: https://github.com/akrhin/mcprig/compare/v0.3.7...v0.3.8
[0.3.7]: https://github.com/akrhin/mcprig/compare/v0.3.6...v0.3.7
[0.3.6]: https://github.com/akrhin/mcprig/compare/v0.3.5...v0.3.6
[0.3.5]: https://github.com/akrhin/mcprig/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/akrhin/mcprig/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/akrhin/mcprig/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/akrhin/mcprig/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/akrhin/mcprig/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/akrhin/mcprig/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/akrhin/mcprig/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/akrhin/mcprig/releases/tag/v0.1.0
