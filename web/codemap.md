# web/

## Responsibility

The panel's whole front end, carried inside the binary and served from memory: a
single-page dashboard for the registry (servers, sets, keys, variables, call
history) with no build step, no framework and no dependencies
(`web/app.js:1-9`). `web/embed.go` is the only Go file here; it exists so the
panel runs from one executable (`web/embed.go:1-3`).

Files: `index.html` (900 lines, markup and dialog skeletons, the `titlebar`
strip of the Win98 theme included), `app.js` (4205, all behaviour),
`app.css` (971, layout/components, colour only via tokens,
`web/app.css:1-8`), `themes.css` (294, colour schemes of both themes, the bevel
tokens of the four Win98 schemes included), `win98.css` (445, the Win98 look,
every rule under `html[data-theme="win98"]`, `prefers-contrast: more`
included), `theme.js` (42, pre-paint theme, palette and language),
`favicon.svg`, `mock/*.json` (demo data).

## Design

**No framework, one closure, one state object.** `app.js` is an IIFE with
`'use strict'` (`web/app.js:10-11`); everything lives in the `st` literal
(`web/app.js:761-776`) — sets, servers, selected set id, health, calls, KPI,
stats filters, dialog state, keys/calls pagination guards. Persisted browser
state is only `mcprig.theme`, `mcprig.palette`, `mcprig.palette.win98`,
`mcprig.lang` and `mcprig.rail`.

**Screens.** `pageOf()` maps the hash to a page: `#/servers`, `#/vars`, else
`sets` (`web/app.js:753-757`, markup `web/index.html:131,406,432`).
A set adds `#/set/<id>` (`web/app.js:760`) and the statistics tab `#/stats/<period>`
(`web/app.js:4118-4119`); `setTab`/`select` rewrite the hash with
`history.replaceState` (`web/app.js:3382-3399`, `:1734-1740`). There is no
`hashchange` listener — navigation happens on click bindings calling
`applyPage` (`web/app.js:1439-1450`) or `setTab`. Set head `#phead`, tabs
`#tab-overview|#tab-calls|#tab-stats` (`web/index.html:142,177-274`), rail
`#rail-sets`, details drawer `#side` (`web/index.html:476`), statusbar
`web/index.html:463-470`. Seven `<dialog>`s: server editor `srv-dlg:500`, server
log `srvlog-dlg:622`, tool quarantine `tools-dlg:645`, keys `keys-dlg:679`,
OAuth `auth-dlg:722`, import/export `io-dlg:747`, prompt `prompt-dlg:800`.

**Rendering is manual DOM with keyed reconciliation.** `el()` builds nodes
(`web/app.js:780-785`); `sync(box, items, key, make, fill)` keeps a
`Map` on the container, patches existing rows in place, inserts new ones, drops
the rest (`web/app.js:867-882`). Every render function goes through it, so a 5 s
tick does not destroy scroll position, focus or an open menu. The statistics
chart is drawn by hand as SVG (`web/app.js:3266-3285`), recomputed on resize
(`web/app.js:3734`).

**i18n** is one `DICT` with `en`/`ru` (`web/app.js:20-685`), `t()` with `{name}`
substitution (`web/app.js:712-718`), Russian plurals via `PLURAL`/`word()`
(`web/app.js:693-710`), static markup translated by walking `data-i18n*`
attributes (`web/app.js:734-748`), numbers and dates through cached `Intl`
formatters (`web/app.js:721-732`). `index.html` holds English text plus keys.

**Themes: two independent axes.** `data-theme` is the look — `default` or
`win98`, rules for the second in `win98.css` — and `data-palette` is its colour
scheme. `theme.js` runs before first paint (`web/index.html:12`) and sets both
on `<html>`; it also migrates a pre-0.6.4 `mcprig.theme` value (`dark`,
`light`, `nord`, `gruvbox`, `mcprig`/`tokyo`, `solarized`, unknown) into
`theme=default` plus that palette, and remembers the last palette of each theme
(`web/theme.js:2-42`). Schemes in `themes.css`: dark on `:root`, then
`html[data-palette="light"|"nord"|"gruvbox"]`, and the four Win98 ones
(`w98`, `w98-cool`, `w98-warm`, `w98-hc`) which add the bevel tokens
`--w98-face/hi/lo/dk/field/sel/title`; `w98-hc` paints them black, white and
yellow and swaps the select arrow to a light one (`web/win98.css:430-438`).
The menu flips either axis and stores both (`web/app.js:3779-3850`).
`prefers-contrast: more` thickens bevels to 3 px in any scheme
(`web/win98.css:441-445`).

**API layer.** `get()` (`web/app.js:945-973`): `fetch` with
`credentials: 'same-origin'`, `Accept: application/json`; `404` becomes `null`,
any other non-ok throws `t('err.http', {status, path})`, a network failure throws
`err.offline`. `send()` (`web/app.js:977-993`) posts JSON, sends `{}` for an
empty POST, reads `data.error` on failure and calls `fail()` unless `quiet`.
There is no 401 branch and no CSRF token in JS: an expired session arrives as a
plain `HTTP 401` error line, while the server redirects navigations to `/login`
(`internal/manager/api.go:449-457`); CSRF is checked server-side from
`Origin`/`Sec-Fetch-Site` plus a JSON-only body rule
(`internal/manager/api.go:712-762`), the session is an `HttpOnly`,
`SameSite=Lax` cookie `mcp_session` (`internal/manager/auth.go:504-525`).

**Demo mode.** `?mock=1` sets `MOCK` (`web/app.js:13`); `mockName()` maps a path
to a JSON file (`web/app.js:888-897`), `mockSend` mutates an in-memory copy,
`mockStats` recomputes slices from call rows so filters match the daemon
(`web/app.js:1042,1291`).

## Flow

1. `theme.js` sets theme and `lang`; deferred `app.js` then boots: `applyStatic`,
   title, rail, theme menu, page, tab, period, `loadState` (`web/app.js:4120-4127`).
2. `loadState()` = `GET /api/state` → `st.sets`, `st.servers`, `st.version`;
   renders chrome, rail, registry, filters; picks the URL's set or the first one
   (`web/app.js:1392-1407`), and `select()` fires `loadHealth`, `loadCalls`,
   `loadLog` (`web/app.js:1750`).
3. Authorization is outside the SPA: unauthenticated `GET /` redirects to
   `/login`, `POST /api/login` sets the cookie (`internal/manager/api.go:415-473`).
4. Polling: `every(ms, fn)` with `document.hidden` guard (`web/app.js:4114`);
   state 5 s, health + calls 5 s (skipped while a second calls page is open), set
   log 10 s while running, `/api/stats` 15 s on the statistics tab, `/api/env`
   30 s (`web/app.js:4128-4141`); health also has a 15 s server-side cache
   (`internal/manager/health.go:19`).
5. Operator action: `act()` disables the button, `POST`s
   `/api/sets/<id>/{start,stop,restart}` and restores the label
   (`web/app.js:3470-3485`, routes `internal/manager/api.go:624`). The POST body
   is not authoritative — callers re-read `loadState()`/`loadHealth()` and the
   `render*` functions paint the new truth from `st`.
6. Dialogs write and then re-read: keys — issue `POST …/keys` (`:4037`), sign
   `PATCH …/keys/<id>` with `{note}` (`:3579`), revoke `DELETE` (`:4077`), with
   `restartError`/`reloadError` as an inline warning; server editor
   `POST /api/servers` / `PUT /api/servers/<name>` prints a backend refusal in
   the form (`web/app.js:2175`, `srvErr` `:2053`); OAuth — `startAuth` posts
   `…/servers/<n>/authorize`, `pollAuth` reads `…/authorize/status` every 1.5 s
   (3 s after a network error) until the flow leaves `pending` (`web/app.js:2009-2036`).
7. Results reach the DOM only through the render functions, all reading `st`:
   `renderRail:1453`, `renderSetServers:1524`, `renderHead:1758`,
   `renderCalls:2640`, `renderLog:2814`, `renderServers:1866`,
   `renderStats:3133`.

## Integration

`web.FS()` (`web/embed.go:16`) wraps `//go:embed index.html app.css themes.css
win98.css app.js theme.js favicon.svg mock/*.json` (`web/embed.go:10`), demo data included.
`internal/manager` takes it once (`panelFS`, `internal/manager/panel.go:16`) and
`servePanel` (`panel.go:28-58`) answers `GET`/`HEAD` only, flat file names or a
`mock/` prefix (`panel.go:40`), whitelisted extensions (`panel.go:17-23`),
`text/html` with the strict CSP (`panel.go:10-11`), `Cache-Control: no-cache`;
it runs from `ServeHTTP` after the host and auth checks
(`internal/manager/api.go:431-437`).

The API is a regex route table (`internal/manager/api.go:582-631`, dispatched by
`serveRoute` `:634`). Endpoints the UI actually calls: `/api/state`,
`/api/sets[/<id>]`, `/api/sets/<id>/{start,stop,restart,servers,keys,
keys/<key>,logs,tools,tools/approve,prompt,servers/<n>/authorize,
servers/<n>/authorize/status,servers/<n>/logs}`, `/api/servers[/<name>]`,
`/api/servers/<name>/probe`, `/api/calls?set&limit&before`, `/api/stats?set&period
&bucket&server&tool&client`, `/api/env[/<NAME>]`, `/api/import`,
`/api/export[/registry]`. Unused by the panel: `/api/browse*` (`api.go:594-598`),
`/api/sets/<id>/reload`, `GET /api/sets` and `GET /api/sets/<id>/keys` (keys come
inside `setView` of `/api/state`).

`mock/*.json` feeds `?mock=1`: `state`, `env`, `calls`, `logs`, `tools`,
`stats-{today,7d,30d,archive}`, and one health file per set id —
`set-<id>-servers.json` for demo, dev, lab, main, ops (`web/app.js:892-893`).

## Discrepancies with docs/en/panel.md

- `panel.md:19` lists a "user" in the top bar; no such element exists
  (`web/index.html:463-470` — crumb, endpoint, Details, theme, language; the
  statusbar holds counts, version, time). `web/app.css:214` still names it.
- `panel.md:31` describes one footer line `N sets · M servers · K running`; the
  UI splits it: rail summary "N sets · K running" (`web/app.js:1477-1481`) and
  statusbar "M servers" (`web/app.js:1412`).
- `panel.md:262,264` document the key field as `label`; the panel now speaks it
  too (`PATCH {label}` `web/app.js:3681`, issue body `body.label` `:4174`, reads
  `k.label` `:3655`); `note` stays accepted on the backend as compatibility
  (`internal/registry/types.go:105-108`).
- `?mock=1` and `web/mock/*.json` are documented nowhere under `docs/`.
