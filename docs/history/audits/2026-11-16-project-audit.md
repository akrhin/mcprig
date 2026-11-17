# Сводный аудит проекта, 2026-11-16 — проблемы по важности

Пять этапов задачи владельца: полевой тест всех MCP → ревью и аудиты → сверка issue → документация и диаграммы → этот список. Источники — по одной строке на находку; детали в исходных отчётах, ссылки в конце.

Как читать: `P1` — ломает работу клиента или вводит в заблуждение при установке; `P2` — ложный успех: отказ проглочен, состояние на экране врёт; `P3` — теряется время на разборе; `P4` — чистота кода и текста; `P5` — косметика.

## 1. Первая очередь — P1 и P2

| # | проблема | где | P | кто нашёл |
| --- | --- | --- | --- | --- |
| 1 | README отправляет за конфигом набора в `~/.config/mcprig/sets/demo/servers.json`, а панель пишет в `<data dir>/sets/<id>/servers.json`. Новичок не подключит stdio-клиента | `README.md:77`, `README.ru.md:76` против `internal/manager/setconfig.go:153-159` | P1 | documenter-live |
| 2 | Тег публикует релиз и GHCR без единой проверки: CI не висит на тег, а прежний gitea-воркфлоу с `needs: [verify, vuln]` удалён | `.github/workflows/ci.yml:7-10`, `release.yml` | P2 | ponytail-review |
| 3 | `images.yml` по `workflow_dispatch` публикует `latest` из любой ветки — прогон на `feat-x` перезаписывает `latest` в реестре хоста нерелизным кодом, job зелёный | `.gitea/workflows/images.yml:10,46,51-56` | P2 | ponytail-review |
| 4 | Битый `registry.json` откладывается в `.corrupt-<unix>`, панель стартует с пустым реестром и без строки в лог: владелец видит «всё пропало», пересоздаёт наборы и ключи | `internal/registry/store.go:39-68`, `internal/manager/api.go:185-187` | P2 | hunter |
| 5 | Правка `env`/`envAllow` сервера: отказ рестарта уходит только в `Warn`, ответ 200 — набор живёт со старым окружением | `internal/manager/api.go:1350-1354,2053-2069` | P2 | hunter |
| 6 | Ротация секрета отвечает `restarted:[ids]`, рестарт идёт в фоне, ошибка в `Warn` — набор работает с отозванным ключом, статус «running», health зелёный | `internal/manager/api.go:1984-2026` | P2 | hunter |
| 7 | Битый `tools.lock.json` теряет одобрения и тут же одобряет все дескрипторы `ApprovedBy:"auto"` до карантина | `internal/mcpserver/quarantine.go:38-62,86-104`, `internal/toollock/toollock.go:66-72` | P2 | arbiter |
| 8 | У `list_servers` нет общего бюджета: 8 слотов × 15 с, шестнадцать зависших серверов = 30 с = бюджет панели | `internal/mcpserver/manager.go:44,77,716-761` | P2 | arbiter |
| 9 | Кнопки жизненного цикла в демо молчат: Stop/Restart/Start/Reload/Delete ничего не меняют, ни ошибки, ни тоста | `web/app.js:1161` | P2 | designer (GUI) |
| 10 | Отказ бэкенда печатается по-русски в английском интерфейсе | `web/app.js:1027`, `internal/mcpserver/resources.go:157` | P2 | designer (GUI) |
| 11 | «Пять мета-тулов» в README, глоссарии, руководстве разработчика и диаграмме; в коде девять | `README.md:19`, `docs/en/glossary.md:11,16`, `docs/en/dev/development.md:27`, `docs/diagrams/mcprig.architecture.en.json:64,268` | P2 | documenter-live |
| 12 | Релизный конвейер описан неверно: goreleaser якобы публикует страницу и образы. Факт — `--skip=publish`, страница от `gh release`, образы только в GHCR, реестр хоста — gitea-воркфлоу | `docs/en/install.md:18`, `docs/en/dev/release.md:30-33` | P2 | documenter-live |

## 2. Серверы набора — что мешает работе агента

| # | проблема | сервер | P | кто нашёл |
| --- | --- | --- | --- | --- |
| 13 | Go не отлаживается на дефолте: адаптер ставит `mode:"debug"`, dlv 1.24.2 пересобирает цель go1.27.4 и падает; агент видит `waiting for initialized event timed out after 10 seconds` | dap-mcp | P2 | полевая полоса |
| 14 | snyk мёртв по двум причинам: whitelist окружения не пропускает `HTTPS_PROXY`, а с прокси упирается в `Snyk Code is not enabled` (403). Круг: `snyk_auth` требует отсутствия `SNYK_TOKEN` | snyk | P2 | полевая полоса |
| 15 | `tools/call` не возвращается, сервер остаётся `healthy:false` с `context deadline exceeded` и уходит в карантин | mermaid-chart | P2 | полевая полоса |
| 16 | Индекс неполный и старый (2722 чанка partial, watcher inactive), пути с прежним именем модуля `github.com/sintez/mcprig` | socraticode | P2 | полевая полоса |
| 17 | Индексов нет ни одного; после `autoindex` поиск работает (202 совпадения за 56 мс) | xerj | P3 | полевая полоса |
| 18 | `detect_antipatterns`/`review_and_gate`/`verify_rendered` отвечают `SCOPE_VIOLATION`: у набора нет MCP roots, хотя SDK v1.8.0 их умеет (`client.go:666`) | designer-skill | P3 | полевая полоса |
| 19 | `container_list(all:true)` — 120 864 байта → режется в невалидный JSON; `docker_raw` без `environment` падает на всех вызовах | portainer-mcp | P3 | полевая полоса |
| 20 | `list_issues` без `type:"issues"` отдаёт PR; `actions_run_read(list_jobs, run_id:1770)` вернул джобы прогона 1630 | gitea | P3 | полевая полоса |
| 21 | test-gap врёт («Untested: runDoctor» при живых тестах), `detail_level:"standard"` — 38 КБ на файл | code-review-graph | P3 | полевая полоса |
| 22 | В реестре «399 tools in 13 toolsets», фактически 54 тула; `projects_list` пуст при 23 контейнерах | arcane-mcp | P3 | полевая полоса |
| 23 | Пин версии в `libraryId` даёт таймаут 120 с (2/2), без пина 1.5 с | context7 | P3 | полевая полоса |
| 24 | `tavily_extract` по битому URL отдаёт пустой успех `Detailed Results:` и `Content: undefined` | tavily-mcp | P3 | полевая полоса |
| 25 | Ищет по докам вендора, спеку не принимает — заменяется `redocly`/`webfetch` | scalar | P3 | полевая полоса |
| 26 | `audit_page` 78/C против `score_page` 68/D на одной странице; `audit_contrast` 37.8 с | raven | P4 | полевая полоса |
| 27 | `debug_step` не ждёт стопа, `status` всегда `running`, `run_to_line` без locals, Python-launch не отдаёт `threadId` | dap-mcp | P3 | полевая полоса |

## 3. Прокси mcprig

| # | проблема | где | P | кто нашёл |
| --- | --- | --- | --- | --- |
| 28 | Whiltelist окружения не пропускает `HTTP(S)_PROXY`/`NODE_USE_ENV_PROXY`: любой stdio-сервер в сеть получает «нет сети» и врёт про аутентификацию | `internal/manager/runtime.go:784,787-820` | P2 | полевая полоса |
| 29 | Обрезка режет payload байтами → невалидный JSON; маркер обещает `/tool-out/<name>` под тем же ключом, MCP-тула для чтения нет | `internal/mcpserver/toolout.go:33,48-84,95` | P3 | полевая полоса |
| 30 | Один таймаут на весь набор (`requestTimeout 120000`), per-call `timeoutMs` нет; текст таймаута без сервера и команды | `internal/config/config.go:786`, `internal/mcpserver/manager.go:1125-1131` | P3 | полевая полоса + hunter |
| 31 | Ожидание слота рапортуется как `таймаут вызова %d мс`, хотя вызов в апстрим не уходил | `internal/mcpserver/invokemany.go:102-110,216-218` | P3 | hunter |
| 32 | `permissions.deny` не фильтрует каталог: `list_commands`/`describe_commands` показывают запрещённое | `internal/mcpserver/manager.go:874,883,917,1016-1017` | P4 | paladin |
| 33 | Каталог без rate limit, `refresh:true` сбрасывает кэш | `internal/mcpserver/ratelimit.go:24-26` | P4 | paladin |
| 34 | `/stats` игнорирует профиль ключа — сосед видит чужую активность и тексты ошибок | `internal/httplisten/httplisten.go:196-208`, `internal/stats/stats.go:283-331` | P4 | paladin |
| 35 | `X-Forwarded-*` доверяются по адресу пира, а не по `x-mcp-proxy-secret` | `internal/manager/auth.go:309-326,374-402` | P4 | paladin |
| 36 | Неизолированный stdio-сервер читает `registry.json`, `env.json`, `sets/*/oauth.json` | `internal/downstream/env.go:14-19`, `internal/sandbox/sandbox.go:132-136` | P3 | paladin |
| 37 | `PUT /api/sets/<id>` коммитит правку и потом отвечает ошибкой, если рестарт не удался | `internal/manager/api.go:1459-1473` | P3 | arbiter |
| 38 | `portBusy` пробует `127.0.0.1`, а набор слушает `listenHost(bindIP)` | `internal/registry/store.go:500-502,670-690` | P3 | arbiter |
| 39 | `oauth/authorize/status` проверяет только наличие сервера, запуск — ещё членство и `URL`: сервер, удалённый из набора, даёт `200 idle`, диалог панели крутится вечно | `internal/manager/oauth.go:39-60,80-92`, `web/app.js:1999,2018` | P3 | ponytail-review |
| 40 | `invoke_many` не ставит `isError`, когда упали все восемь; дубликаты имён серверов в наборе принимаются; reload остановленного набора без ключа → 500 | `invokemany.go:77-116`, `registry/validate.go:383-398`, `manager/api.go:1545-1558` | P4 | arbiter |
| 41 | Сбой OpenBao подменяется `env.json`; сбой `os.Remove` в свипе не считается; просмотр глотает ошибки `ListTools`/`ListResources`/`ListPrompts` | `manager/secrets.go:382-401`, `toolout/toolout.go:169-196`, `manager/browse.go:171,185,190` | P3 | hunter |
| 42 | `list_resources`/`list_prompts` пусты для всех 18 серверов; `list_servers` не показывает URL, env, урезание тулов | `internal/mcpserver/manager.go`, `resources.go` | P4 | полевая полоса |

## 4. Панель и тема

| # | проблема | где | P | кто нашёл |
| --- | --- | --- | --- | --- |
| 43 | Контраст ниже AA: `:9104 · running` 4.12 при 11 px, `not probed` 4.38, светлая схема `--text-mute` на рейле 4.26 | `web/themes.css:44`, `web/app.js:299` | P3 | designer (GUI) |
| 44 | Цели нажатия 24 px в строках таблицы, на 375 px их 55 | `web/app.css:431` | P3 | designer (GUI) |
| 45 | Время вызова и «last call» только `HH:MM`, без даты; на mobile подпись без дня | `web/app.js:848-856,1636` | P3 | designer (GUI) |
| 46 | Пустое состояние фильтра врёт о причине («nothing found» при пустом наборе) | `web/app.js:2719` | P3 | designer (GUI) |
| 47 | Одно состояние под тремя именами: `not probed` / `0 not checked` / docs «Not checked» | `web/app.js:299,119`, `docs/en/panel.md:34` | P3 | designer (GUI) |
| 48 | `TOOLS —` во всех строках запущенного набора, хотя шапка обещает `TOOLS 117`; смысл только в tooltip | `web/app.js:1635` | P3 | designer (GUI) |
| 49 | Подвал панели описан неверно (фактически серверы, версия, «обновлено») | `docs/en/panel.md:31` против `web/index.html:498-505` | P3 | documenter-live |
| 50 | `/stats` в architecture без оговорки, что открытый набор отдаёт анонимный редактированный снимок | `docs/en/architecture.md:159` | P3 | documenter-live |
| 51 | OAuth-путь исправлен наполовину: `install.md:180-184` и `panel.md:200` верны, `architecture.md:173` и `configuration.md:317` — старый | docs/en | P3 | documenter-live |
| 52 | Примеры версии закреплены на 0.4.0 при 0.6.3 | `README.md:34-35`, `docs/en/install.md:95-106` | P3 | documenter-live |
| 53 | Вкладки не в адресе, незнакомый hash глотается; пустой диалог OAuth; бледные placeholder'ы 3.73:1 | `web/app.js:3406-3418,3748,2021-2025` | P4 | designer (GUI) |

## 5. Код и тесты

| # | проблема | где | P | кто нашёл |
| --- | --- | --- | --- | --- |
| 54 | `toollock` покрыт на 50 %: ветки разбора битого `tools.lock.json` не тестируются — там же дефект R1 | `internal/toollock/toollock.go:66-72`, `quarantine_test.go:325,361` | P2 | proctor |
| 55 | Тест закрепляет дефект R4: ждёт 500 после уже закоммиченной правки | `internal/manager/manager_test.go:804-816` | P2 | proctor |
| 56 | Один блок флагов панели объявлен четырежды; хелперы-близнецы (`warm`≡`health`, `doctor`≡`runtime`), пять копий плюрала | `cmd/mcprig/approve.go:36-39`, `logs.go:53-56`, `status.go:76-79`, `warm.go:71-74,277-287`, `doctor.go:198-239,650-660` | P4 | janitor |
| 57 | Мёртвый код: `newTokenSet`, `hashPassword`, `Callback.URL`, `QueryHistory` (подтверждено `deadcode`), плюс `Handler.Addr`, `Handler.fetch`, `Store.Delete` | `internal/httplisten/httplisten.go:77`, `manager/auth.go:83`, `oauth/callback.go:91`, `stats/history_query.go:362` | P4 | janitor + codemap |
| 58 | Монолиты: `internal/manager/api.go` 2168 строк / 89 функций, `internal/mcpserver/manager.go` 1862 / 71 | — | P4 | janitor |
| 59 | Протокол `.owner` размазан по трём пакетам | `mcpserver/toolout.go:233`, `httplisten/httplisten.go:238-239`, `toolout/toolout.go:142` | P4 | janitor |
| 60 | `gosec` `G301/G302/G306` исключены глобально, хотя все записи уже `0600/0700` — новая правка с `0o644` пройдёт CI | `.golangci.yml:24-26` | P3 | ponytail-review |
| 61 | `deploy/README.md` и `deploy/mcprig-warm.service` описывают не тот каталог / шаблон `%i` без `@` в имени | `deploy/` | P4 | janitor + codemap |
| 62 | `?mock=1` и `web/mock/*.json` вшиты в бинарь и нигде не задокументированы | `web/embed.go:13`, `internal/manager/panel.go:40` | P4 | codemap |
| 63 | Ключ панели шлёт legacy `note`, бэкенд принимает `label` и `note` | `web/app.js:3554,3580`, `internal/registry/types.go:101-108` | P3 | codemap |
| 64 | `mcprig panel --help` уходит в `usageError` и exit 2 | `cmd/mcprig/main.go:189-193` | P4 | codemap |
| 65 | Ретенция raw: таблица обещает «today + 7 days», читается 7, на диске 8 файлов | `internal/stats/history.go:35-36,306` | P5 | codemap |
| 66 | Устаревшие комментарии о составе панели | `web/app.js:3-4`, `web/embed.go:13` | P5 | codemap |

## 6. Инструменты (проверено 2026-11-17)

Поставлены по слову владельца: `golangci-lint` 2.15.0 (та же версия, что в CI), `staticcheck` 2026.2.2, `deadcode` — в `~/go/bin`. Прогон по master: `golangci-lint run` — 0 issues (конфиг принят), `staticcheck ./...` — 0 находок, `deadcode ./...` — 4 недостижимые функции (см. #57). Прежние бинари были непригодны: `staticcheck` собран go1.25 и падал на модуле с go1.27.4.

Веб-дедкода инструментом нет: `package.json` в проекте отсутствует, knip без манифеста бесполезен; фронт проверяется грепом (как нашло GUI-ревью — все 207 `$('id')` живые).

## 7. Что проверено и чисто

- Контрактные блокеры безопасности закрыты: fail-closed авторизация, токены не уходят клиенту, allowlist окружения, Origin/Host/CSRF, `/tool-out` 404 на чужой ключ, права одинаковы для `invoke_command` и `invoke_many`.
- `go test ./... -race -p 1` зелёный (15 пакетов), `scripts/sandbox-e2e.sh` 14/14, `scripts/docs-check.sh` 5/5, `go vet` 0.
- Лимиты `invoke_many` 8/4 и освобождение слотов, изоляция ключей, restart/backoff 5 попыток с потолком 30 с, чистка stdio-групп и `/tool-out`.
- Issue #5 (пустой набор) и #6 (выбор сервера из реестра) решены в 0.6.3; #7 частично; #8 не сделано.

## 8. Источники

`2026-11-16-mcp-field-test.md` · `2026-11-16-review-code.md` · `2026-11-16-security.md` · `2026-11-16-maintainability.md` · `2026-11-16-tests.md` · `2026-11-16-error-paths.md` · `2026-11-16-panel-gui.md` · `2026-11-16-ponytail-review.md` · `2026-11-16-docs.md` · `2026-11-16-codemap-drift.md` · `2026-11-16-win98-comparison.md`.

## 9. Полосы починки

Расписано по исполнителям так, чтобы полосы не пересекались по файлам.

| полоса | исполнитель | пункты | файлы | ворота |
| --- | --- | --- | --- | --- |
| прокси | fixer | 7, 28, 29, 30, 31, 32, 33, 38, 40, 42 | `internal/mcpserver`, `downstream`, `config`, `registry`, `toolout` | `golangci-lint`, тесты этих пакетов, `go vet` |
| менеджер и тесты | fixer | 4, 5, 6, 39, 41 (свип), 54, 55 | `internal/manager/api.go`, `oauth.go`, тесты, `toolout` | то же для `manager` и `toolout` |
| CI и линт | оркестратор | 2, 3, 60 | `.github/workflows/release.yml`, `.gitea/workflows/images.yml`, `.golangci.yml` | `golangci-lint run` |
| тема и панель | designer | 9–10, 43–48, 53 + гибрид Win98 и адаптивность | `web/*` | контраст AA в четырёх схемах, нули радиусов и теней, 375/768/1280 без скролла, `docs-check` |
| документация и диаграммы | documenter-live | 1, 11, 12, 49–52, 8 (диаграмма) | `README*`, `docs/en`, `docs/ru`, `docs/diagrams` | `docs-check`, archify finalize |
| мёртвый код и мелочи | fixer | 13–14 (dap), 56, 57, 59, 61, 62, 63, 64, 65, 66 | `cmd/mcprig`, `internal/*`, `deploy/`, `docs` | `deadcode` пусто, `golangci-lint`, `go test` |
| серверы и хост | оркестратор | 15–27, 35–37, 43–44 (инфра) | реестр набора `main`, хостовые сервисы | живой вызов каждого исправленного сервера |
| живые проверки | оркестратор + designer | 9–10 на настоящем бэкенде, 54 (e2e), установка с нуля | — | прогон клиентом, а не `invoke_command` |

Порядок: прокси, менеджер и тема — параллельно (разные файлы); после них документация, мёртвый код, хост; живые проверки последними, когда код на месте.

