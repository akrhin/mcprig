# web/

## Responsibility

The panel's whole front end, carried inside the binary and served from memory: a
single-page dashboard for the registry (servers, sets, keys, variables, call
history) with no build step, no framework and no dependencies
(`web/app.js:1-9`). `web/embed.go` is the only Go file here; it exists so the
panel runs from one executable (`web/embed.go:1-3`).

Files: `index.html` (984 lines, markup and dialog skeletons, the `titlebar`
strip of the Win98 theme included), `app.js` (4867, all behaviour),
`app.css` (1097, layout/components, colour only via tokens,
`web/app.css:1-8`), `themes.css` (302, colour schemes of both themes, the bevel
tokens of the four Win98 schemes included), `win98.css` (459, the Win98 look,
every rule under `html[data-theme="win98"]`, `prefers-contrast: more`
included), `theme.js` (42, pre-paint theme, palette and language),
`favicon.svg`, `mock/*.json` (demo data).

## Design

**No framework, one closure, one state object.** `app.js` is an IIFE with
`'use strict'` (`web/app.js:10-11`); everything lives in the `st` literal
(`web/app.js:886-915`) — sets, servers, selected set id, health, calls, KPI,
stats filters, dialog state, keys/calls pagination guards. Persisted browser
state is `mcprig.theme`, `mcprig.palette`, `mcprig.palette.win98`,
`mcprig.lang`, `mcprig.rail` and the statistics filters
`mcprig.stats.{server,tool,client}` (`web/app.js:907-908,4426-4432`).

**Screens.** The address is the state: `pageOf()` maps the hash to a page
(`#/` host, `#/sets`, `#/stats/<period>`, `#/set/<id>[/<tab>]`, `#/servers`,
`#/vars`, `web/app.js:861-868`), `routeOf()` adds the set and its tab
(`:872-882`, unknown tab to overview, `#/sets` with no set is the list),
`applyPage()` shows one page and repaints the page nav (`:1737-1755`). On load the
address is read once into the initial state (`routeOf()` at `:885`), so the first
`loadState()` renders that set (`st.boot`); afterwards `route()` follows every
`hashchange` (`:4378-4392`).
`setTab()` pushes `#/set/<id>/<tab>` (`:4017-4033`), `select()` pushes the set
address and opens the set page on an explicit choice (rail click, create,
delete of the open set) even from the host overview (`:2021-2072`);
`setHash()` builds it (`:2074-2076`), `setTitle()` names the page (`:2079-2087`).
The set list at `#/sets` is `renderSetsList()` (`:1704-1735`), the host overview
`refreshHost()`/`renderHostCalls()` (`:2238-2284`, `:2386-2417`). Set head `#phead`,
tabs `#tabs` with six buttons (`web/index.html:271,313-320`) and the panels
`#tab-overview|#tab-servers|#tab-keys|#tab-tools|#tab-calls|#tab-logs`
(`web/index.html:322,354,389,423,451,487`), rail `#rail` (`web/index.html:30-58`),
statusbar `web/index.html:698-706`. Pages: host `#page-host:190`, sets
`#page-sets:250`, statistics `#page-stats:503`, all servers `#page-servers:641`,
variables `#page-vars:667`. Five `<dialog>`s: server editor `srv-dlg:711`, server
log `srvlog-dlg:833`, OAuth `auth-dlg:856`, import/export `io-dlg:881`, prompt
`prompt-dlg:934`; keys and tools are tabs of the set, not dialogs.

**Rendering is manual DOM with keyed reconciliation.** `el()` builds nodes
(`web/app.js:919-924`); `sync(box, items, key, make, fill)` keeps a
`Map` on the container, patches existing rows in place, inserts new ones, drops
the rest (`web/app.js:1030-1045`). Every render function goes through it, so a 5 s
tick does not destroy scroll position, focus or an open menu. The statistics
chart is drawn by hand as SVG (`web/app.js:3916-4001`), recomputed on resize
(`web/app.js:4394-4401`).

**i18n** is one `DICT` with `en`/`ru` (`web/app.js:20-803`), `t()` with `{name}`
substitution (`web/app.js:815-822`), Russian plurals via `PLURAL`/`word()`
(`web/app.js:805-813`), static markup translated by walking `data-i18n*`
attributes (`web/app.js:837-851`), numbers and dates through cached `Intl`
formatters (`web/app.js:827-836`). `index.html` holds English text plus keys.

**Themes: two independent axes.** `data-theme` is the look — `default` or
`win98`, rules for the second in `win98.css` — and `data-palette` is its colour
scheme. `theme.js` runs before first paint (`web/index.html:13`) and sets both
on `<html>`; it also migrates a pre-0.6.4 `mcprig.theme` value (`dark`,
`light`, `nord`, `gruvbox`, `mcprig`/`tokyo`, `solarized`, unknown) into
`theme=default` plus that palette, and remembers the last palette of each theme
(`web/theme.js:2-42`). Schemes in `themes.css`: dark on `:root`, then
`html[data-palette="light"|"nord"|"gruvbox"]` (`web/themes.css:46,77,109`), and
the four Win98 ones (`w98`, `w98-cool`, `w98-warm`, `w98-hc`,
`web/themes.css:145,184,223,265`) which add the bevel tokens
`--w98-face/hi/lo/dk/field/sel/title`; `w98-hc` paints them black, white and
yellow and swaps the select arrow to a light one (`web/win98.css:430-436`).
The menu flips either axis and stores both (`web/app.js:4456-4490`).
`prefers-contrast: more` thickens bevels to 3 px in any scheme
(`web/win98.css:439-443`).

**API layer.** `get()` (`web/app.js:1115-1145`): `fetch` with
`credentials: 'same-origin'`, `Accept: application/json`; `404` becomes `null`,
any other non-ok throws `t('err.http', {status, path})`, a network failure throws
`err.offline`. `send()` (`web/app.js:1149-1173`) posts JSON, sends `{}` for an
empty POST, reads `data.error` on failure and calls `fail()` unless `quiet`.
There is no 401 branch and no CSRF token in JS: an expired session arrives as a
plain `HTTP 401` error line, while the server redirects navigations to `/login`
(`internal/manager/api.go:451-458`); CSRF is checked server-side from
`Origin`/`Sec-Fetch-Site` plus a JSON-only body rule
(`internal/manager/api.go:716-769`), the session is an `HttpOnly`,
`SameSite=Lax` cookie `mcp_session` (`internal/manager/auth.go:496-505`).

**Demo mode.** `?mock=1` sets `MOCK` (`web/app.js:13`); `mockName()` maps a path
to a JSON file (`web/app.js:1058-1067`), `mockSend` mutates an in-memory copy,
`mockStats` recomputes slices from call rows so filters match the daemon
(`web/app.js:1214-1406`, `:1498-1586`).

## Flow

1. `theme.js` sets theme and `lang`; deferred `app.js` then boots: `applyStatic`,
   title, rail, theme menu, page, tab, period, `loadState`
   (`web/app.js:4837-4867`).
2. `loadState()` = `GET /api/state` → `st.sets`, `st.servers`, `st.version`;
   renders chrome, rail, registry, filters; picks the URL's set or the first one
   (`web/app.js:1616-1657`, stale answers dropped by `stateSeq`), and `select()` fires `loadHealth`, `loadCalls`,
   `loadLog`, `loadTools` (`web/app.js:2021-2072`).
3. Authorization is outside the SPA: unauthenticated `GET /` redirects to
   `/login`, `POST /api/login` sets the cookie (`internal/manager/api.go:451-466`,
   `:479`).
4. Polling: `every(ms, fn)` with `document.hidden` guard (`web/app.js:4835`);
   state 5 s, health + calls 5 s (skipped while a second calls page is open), set
   log 10 s while running and only on the logs tab, `/api/stats` 15 s on the
   statistics tab with `refreshHost()` on the host page, `/api/env`
   30 s (`web/app.js:4850-4866`); health also has a 15 s server-side cache
   (`internal/manager/health.go:21-22`).
5. Operator action: `act()` disables the button, `POST`s
   `/api/sets/<id>/{start,stop,restart}` and restores the label
   (`web/app.js:4091-4109`, route `internal/manager/api.go:628`). The POST body
   is not authoritative — callers re-read `loadState()`/`loadHealth()` and the
   `render*` functions paint the new truth from `st`.
6. Dialogs write and then re-read: keys — issue `POST …/keys` (`:4753`), sign
   `PATCH …/keys/<id>` with `{label}` (`:4196-4211`; it leaves `st.busy` alone so
   the host tick cannot swallow the save), revoke `DELETE` (`:4790`), with
   `restartError`/`reloadError` as an inline warning; server editor
   `POST /api/servers` / `PUT /api/servers/<name>` prints a backend refusal in
   the form (`web/app.js:2629-2635,2767-2798`); OAuth — `startAuth` posts
   `…/servers/<n>/authorize`, `pollAuth` reads `…/authorize/status` every 1.5 s
   (3 s after a network error) until the flow leaves `pending` (`web/app.js:2585-2612`).
7. Results reach the DOM only through the render functions, all reading `st`:
   `renderRail:1757`, `renderSetServers:1829`, `renderHead:2089`,
   `renderCalls:3222`, `renderLog:3410`, `renderServers:2442`,
   `renderStats:3760`.
8. A read failure is shown, not swallowed: `loadLog` puts `data.error` into
   `#log-err` under `t('log.fail')` (`web/app.js:3393-3404`), `loadSrvLog` says
   "gone" when the panel answers `404` (`data === null`) and otherwise prints the
   reason (`:3458-3488`), and `refreshHost` keeps `null` for a failed set, so the
   host tiles show a dash instead of zero (`web/app.js:2238-2284`,
   `tileWord` at `:2184-2194`).

## Integration

`web.FS()` (`web/embed.go:16`) wraps `//go:embed index.html app.css themes.css
win98.css app.js theme.js favicon.svg mock/*.json` (`web/embed.go:10`), demo data included.
`internal/manager` takes it once (`panelFS`, `internal/manager/panel.go:16`) and
`servePanel` (`panel.go:28-57`) answers `GET`/`HEAD` only, flat file names or a
`mock/` prefix (`panel.go:36`), whitelisted extensions (`panel.go:18-24`),
`text/html` with the strict CSP (`panel.go:13-14,50`), `Cache-Control: no-cache`;
it runs from `ServeHTTP` after the host and auth checks
(`internal/manager/api.go:439`).

The API is a regex route table (`internal/manager/api.go:582-636`, dispatched by
`serveRoute` `:638`). Endpoints the UI actually calls: `/api/state`,
`/api/sets[/<id>]`, `/api/sets/<id>/{start,stop,restart,servers,keys,
keys/<key>,logs,tools,tools/approve,prompt,servers/<n>/authorize,
servers/<n>/authorize/status,servers/<n>/logs}`, `/api/servers[/<name>]`,
`/api/servers/<name>/probe`, `/api/calls?set&limit&before`, `/api/stats?set&period
&bucket&server&tool&client`, `/api/env[/<NAME>]`, `/api/import`,
`/api/export[/registry]`. Unused by the panel: `/api/browse*` (`api.go:598-603`),
`/api/sets/<id>/reload`, `GET /api/sets` and `GET /api/sets/<id>/keys` (keys come
inside `setView` of `/api/state`).

`mock/*.json` feeds `?mock=1`: `state`, `env`, `calls`, `logs`, `tools`,
`stats-{today,7d,30d,archive}`, and one health file per set id —
`set-<id>-servers.json` for demo, dev, lab, main, ops (`web/app.js:1062-1063`).
The demo is documented in `docs/en/panel.md:56-59`.
