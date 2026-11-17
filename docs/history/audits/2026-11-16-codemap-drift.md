# Долги по карте кода (codemap), 2026-11-16

Снимок. Источник — проход по 16 каталогам репозитория для карты кода
(`codemap.md` в корне и подкаталогах, состояние `.slim/codemap.json`). Каждый
пункт ниже — расхождение кода с документацией, мёртвый код или ловушка
сопровождения, найденные при чтении. Ничего из этого не исправлено.

Приоритет тот же, что в [ревью 2026-11-15](2026-11-15-review-v0.5.1.md):
P1 — цифры врут или пользователь в тупике; P2 — тихие отказы, неверное
описание безопасности, диагностика; P3 — расхождение документации с
поведением; P4 — ловушки сопровождения; P5 — мелочи.
Объём: S ≤ полдня, M ≤ 2 дня, L — отдельная фаза.

## 1. Документация расходится с кодом

| ID | долг | источник | P | объём | риск |
| --- | --- | --- | --- | --- | --- |
| D1 | «Оба health-endpoint панели публичные» — публичен только `/healthz`; `/api/sets/<id>/servers` требует сессии | `docs/en/panel.md:442` vs `internal/manager/api.go:402`, `api.go:431` | P2 | S | низкий |
| D2 | «`GET /stats` защищён тем же bearer» — у открытого набора снимок отдаётся анонимно и редактируется (`SnapshotRedacted`) | `docs/en/architecture.md:159` vs `internal/httplisten/httplisten.go:193-207` | P2 | S | низкий |
| D3 | Токены OAuth лежат в `<data dir>/sets/<id>/oauth.json`, а не в `<data dir>/oauth.json`; верна только страница `cli.md` | `docs/en/configuration.md:317`, `docs/en/architecture.md:173` vs `internal/manager/oauth.go:101` | P3 | S | низкий |
| D4 | Сторож stdio-ребёнка (`child-guard`, `Pdeathsig`, добивание внуков) в `docs/en/` не описан вовсе — только в архиве аудита | `internal/downstream/guard.go:23`, `internal/downstream/procattr_linux.go:10`; упоминание лишь в `docs/history/audits/2026-11-15-review-v0.5.1.md` | P3 | M | низкий |
| D5 | В панели нет «user» в верхней панели и футер не одной строкой: счётчики разнесены по рейлу и статусбару | `docs/en/panel.md:19`, `:31` vs `web/app.js:1412`, `:1477-1481`, устаревший комментарий `web/app.css:214` | P4 | S | низкий |
| D6 | Поле ключа называется `label`, а панель шлёт legacy `note`; бэкенд принимает оба — выбрать одно имя | `docs/en/panel.md:262,264` vs `web/app.js:3554,3580`, `internal/registry/types.go:101-108` | P3 | S | низкий |
| D7 | `?mock=1` и `web/mock/*.json` вшиты в бинарник и отдаются панелью, но не задокументированы нигде | `web/embed.go:13`, `internal/manager/panel.go:40` | P4 | S | низкий |
| D8 | Пример вывода `warm` со старым согласованием числа («421 инструмент(ов)»); код печатает формы через `pluralBy` | `docs/en/cli.md:318-319`, `docs/ru/cli.md:319-320` vs `cmd/mcprig/warm.go:436,438` | P5 | S | низкий |
| D9 | Ретенция сырого журнала: таблица «today + 7 days», читается 7 дней, на диске остаётся 8 файлов — формулировку уточнить | `docs/en/panel.md:390` vs `internal/stats/history.go:35-36`, `:306` | P5 | S | низкий |
| D10 | `mcprig panel --help` не help: любой аргумент, включая `-h`, уходит в `usageError` и exit 2 | `cmd/mcprig/main.go:189-193`; в `docs/en/cli.md:129-131` только «No flags» | P4 | S | низкий |

## 2. Код

| ID | долг | источник | P | объём | риск |
| --- | --- | --- | --- | --- | --- |
| D11 | Мёртвый экспорт: `Handler.Addr`, `Handler.fetch`, `Store.Delete` — вызовов вне пакета нет | `internal/oauth/handler.go:25,29`, `internal/oauth/store.go:134` | P4 | S | низкий |
| D12 | `healthTTL`/`healthTimeout` продублированы в двух пакетах; связь держат только комментарий и тест — вынести в общий контракт | `internal/manager/health.go`, `internal/mcpserver/manager.go:36,44` | P4 | S | низкий |
| D13 | Имена пакетов путают: `internal/toolout` — ретенция и `.owner`, а обрезка ответов живёт в `internal/mcpserver/toolout.go` | оба `codemap.md` | P4 | S | низкий |
| D14 | `deploy/mcprig-warm.service` — шаблон `%i`, но имя файла без `@`: установка по имени из шапки файла | `deploy/mcprig-warm.service:1-3`, `deploy/README.md:10` | P4 | S | низкий |
| D15 | Устаревшие комментарии: `app.js` про `/api/calls` каждые 15 с; `embed.go` про «без каталога `mock/`» | `web/app.js:3-4`, `web/embed.go:13` | P5 | S | низкий |

## 3. Вне репозитория

| ID | долг | источник | P | объём | риск |
| --- | --- | --- | --- | --- | --- |
| D16 | Глобальный `AGENTS.md` хоста описывает mcprig как «четыре мета-тула» и не знает `invoke_many`; в коде их девять (пять рабочих + четыре resource/prompt) | `~/.config/opencode/AGENTS.md` vs `internal/mcpserver/manager.go:560+`, `internal/mcpserver/invokemany.go:51` | P3 | S | низкий |

## 4. Проверено и долгом не является

- `deny` в правах набора — не подстрока, а якорная case-insensitive glob
  (`internal/config/permissions.go:71-114`); документация говорит то же.
- Токены в `registry.json` — открытым текстом в файле `0600`; sha256 только при
  сверке (`internal/httplisten/httplisten.go:84-101`). Документация это и пишет.
- Число мета-тулов в `README.md:83` и `docs/en/architecture.md:21` (девять)
  совпадает с кодом.
- Маршрутная таблица панели (28 записей), скриншот и токен-префиксная модель в
  документации соответствуют коду.

## 5. Статус на 2026-11-17

Проверено по файлам, а не по памяти. «Закрыт» — расхождения больше нет;
«остался» — не тронуто; «вне репозитория» — правится не здесь.

| ID | статус | где |
| --- | --- | --- |
| D1 | закрыт | `docs/en/panel.md:288`, `:490` — публичен назван только `GET /healthz` |
| D2 | закрыт | `docs/en/architecture.md:187-188` — `/healthz` публичный, `/stats` под bearer, у открытого набора снимок редактируется |
| D3 | закрыт | `docs/en/configuration.md:318-319`, `docs/en/architecture.md:175` (и RU-зеркала) уже называют `sets/<id>/oauth.json`; сверено с `internal/manager/oauth.go:102` и `cmd/mcprig/main.go:258`, `:508`, `warm.go:176`, `auth.go:93` (`stateDir()`) |
| D4 | закрыт этой полосой | `docs/en/architecture.md` и `docs/ru/architecture.md`, раздел «Process supervision» / «Присмотр за процессами»: `GuardMain`, `RunChildGuardMode`, `Pdeathsig`, `Setpgid`, добивание группы, поведение без сторожа, отличие не-Linux |
| D5 | закрыт этой полосой | `docs/en/panel.md:19` и `docs/ru/panel.md:19` — топбар по `web/index.html`: крошки, адрес с копированием, ящик деталей, меню оформления с двумя осями, язык, статичный чип `admin`; футер сверен с `web/app.js:1516-1517`, `:1573-1577` и оставлен как есть |
| D6 | **закрыт** полосой `2026-11-17-fix-minor-debts` | панель шлёт `label` и при выдаче (`POST …/keys` с `{"label":…,"profile":…}`), и при правке (`PATCH …/keys/<key>` с `{"label":…}`); `note` остался только именем приёма на бэкенде (`internal/manager/api.go:1835-1855`, `internal/registry/types.go:105-108`). Документация: `docs/{en,ru}/panel.md`, раздел про **Ключи** |
| D7 | закрыт этой полосой | `docs/en/panel.md` и `docs/ru/panel.md`, раздел «Demo mode» / «Демо-режим»: `?mock=1`, `web/mock/*.json`, `web/embed.go`, `internal/manager/panel.go:36`, вымышленные данные, правки только в памяти |
| D8 | закрыт | `docs/en/cli.md:318-319` — пример `421 инструмент`, форма через `pluralBy` |
| D9 | закрыт этой полосой | `docs/en/panel.md:415`, `:422-427` (и RU): сырой журнал — `сегодня + 7` = восемь файлов, удаляется день старше `today − 7`, период `7d` читает сегодня и шесть суток до него (`internal/stats/history.go:35-36`, `:306`, `history_query.go:372-379`) |
| D10 | **закрыт** полосой `2026-11-17-fix-minor-debts` | `mcprig panel -h` / `--help` печатает двадцать переменных окружения и выходит с кодом `0`, любой другой аргумент — ошибка с кодом `2` (`cmd/mcprig/main.go:105-143`); `docs/en/cli.md:139` и `docs/ru/cli.md:136` переписаны |
| D11 | закрыт этой полосой | `Handler.Addr` и `Store.Delete` удалены, тесты переписаны без них; `Handler.fetch` назван тестовым швом (`internal/oauth/handler.go:22-26`). Подробности — [2026-11-17-fix-naming-debts.md](2026-11-17-fix-naming-debts.md) |
| D12 | остался | дубль `healthTTL`/`healthTimeout` — код |
| D13 | закрыт этой полосой | переименование отклонено: 9 импортов, 24 обращения `toolout.X`, 34 упоминания в `.md` без изменения поведения; расхождение имён записано в шапке `internal/mcpserver/toolout.go` и в `internal/toolout/codemap.md`. Подробности — [2026-11-17-fix-naming-debts.md](2026-11-17-fix-naming-debts.md) |
| D14 | закрыт | `deploy/mcprig-warm.service:1-3` называет установку как `mcprig-warm@.service`; `deploy/README.md:10` описывает его шаблонным |
| D15 | **закрыт** полосой `2026-11-17-fix-minor-debts` | `web/app.js:3-5` больше не говорит про `/api/calls` раз в 15 с, `web/embed.go:12-14` объясняет, что `mock/` вшит и зачем |
| D16 | вне репозитория, снят | глобальный `AGENTS.md` хоста с 17.11 говорит про девять мета-тулов и `isError: true` |

Карта кода: `codemap.mjs update` прогнан, `changes` отвечает «No changes detected»;
устаревшие `file:line` в `internal/mcpserver/codemap.md` сверены с `grep -n` и
исправлены по всему файлу (Design, Flow, Integration).
