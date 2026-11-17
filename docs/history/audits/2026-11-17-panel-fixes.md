# Правки панели по аудиту 2026-11-16

Закрытие восьми находок [аудита GUI](2026-11-16-panel-gui.md) в коде панели.
Зона — только `web/` (`app.js`, `app.css`, `index.html`, `themes.css`,
`win98.css`): `internal/`, `cmd/`, `docs/` и `CHANGELOG.md` правили другие
полосы, их не трогал.

Смотрел по реальному рендеру: статика `python3 -m http.server 9099` из `web/`,
адрес `?mock=1` (демо-данные из `web/mock/*.json`, бэкенд не нужен), ширина
1280 и 375, языки EN и RU, все восемь схем (dark, light, nord, gruvbox,
w98, w98-cool, w98-warm, w98-hc).

Ворота: `node --check web/app.js`, `go build ./...`,
`bash scripts/docs-check.sh`, отсутствие горизонтальной прокрутки на обеих
ширинах, пустая консоль, нулевые радиусы/тени/градиенты в win98, паритет
ключей EN/RU — всё зелёное, подробности в разделе «Ворота».

## По пунктам

| № | Пункт | Что сделано | Чем проверено |
|---|-------|-------------|---------------|
| 1 | P2 молчащие кнопки демо (`web/app.js:1161`) | В `mockSend` добавлена ветка `/api/sets/<id>[/start\|stop\|restart\|reload]` и `DELETE`: набор реально меняет `running`, `pid`, `startedAt`, чистит `error`/`exitCode`/`restarts`; DELETE убирает набор из `st.mock.sets`, ошибка уходит в `fail()`. `loadState()` видит мутацию через `GET /api/state` (`web/app.js:974`) | Stop → бейдж `stopped`, рейл «main :9104 · stopped», кнопка «Start», TOOLS «—»; Start → `running`, TOOLS «?»; Delete set demo → 4 набора в рейле, `#rail-count` «4», переход на соседа, `#errbar` пуст; Restart у `lab` (failed) → `running` |
| 2 | P2 русский отказ в EN-панели (`web/app.js:1027`) | `mockServerErr` возвращает `{k, v}` вместо русских строк; call site прогоняет через `t(k,v)` и `fail()`. 12 ключей `mock.*` добавлены в обе таблицы (EN/RU) | EN: «name: lowercase latin letters, digits, - and _», «server serena already exists». RU: «name: строчные латинские буквы, цифры, - и _». Пропущенных ключей нет — «undefined» не показывается |
| 3 | P3 время без даты (`web/app.js:848-856,1636`) | Новая `clockAt(v)`: сегодня — `clock()` (HH:MM:SS), иначе «дд/мм, чч:мм»; `hm(ms)` оставлена тикам графика. Таблица вызовов — `clockAt(c.time)` + `title` с полной датой; колонка Last call — `clockAt(last[row.name])` | Вызовы: «11/08, 23:14»; Last call: «11/08, 23:13». Сегодняшние строки по-прежнему с секундами |
| 4 | P3 пустой фильтр врёт о причине (`web/app.js:2719`) | `renderCalls`: `filtered = !!(term \|\| onlyErr)`; при фильтре — `calls.noMatch` + новый `calls.noMatchText`, без фильтра — прежний `calls.emptyText` | Запрос «zzzz» → «0 of 100», заголовок «nothing found — loosen the filter», тело «Calls are there: clear the search or uncheck “errors only”.». RU: «ничего не найдено — ослабьте фильтр» |
| 5 | P3 контраст 4.12 при 11px (`web/themes.css:44`) | `--text-mute`: dark `#8b98a7`, light `#5b6672`, nord `#b4bece`, gruvbox `#b3a694`; для своего 14 %-тинта бейджа: light `--ok #197a4c→#166b43`, nord `--err→#f0bcc0`, gruvbox `--err→#fd7f6c`, gruvbox `--accent→#90b2a4` | Свой расчёт по рендеру с учётом полупрозрачных подложек: 8 схем × 2 страницы, 173–175 текстовых узлов на схему, **FAIL 0**, минимум 4.6 (nord «not checked»); было 4.12/4.17/3.46/3.68. Попутно: `::placeholder` = `--text-mute` (было браузерное 3.73), `.dot-hollow` 7 px с кольцом 1.5 px (5–6 px читался как буква «o» в «o not checked») |
| 6 | P3 цели нажатия 24px (`web/app.css:431`) | `.btn-xs` → высота 32 px (горизонтальный padding оставлен 8 px, иначе таблица вылезает), `.icon-xs` → 32×32, `.table td` padding 8 px (строки не раздулись) + `web/win98.css`: `.btn-sm/.btn-xs` 24 px, `.ghost-icon` 24 px | 375: «check» 50×32, «×» 32×32; 1280: то же, таблица на месте, `scrollWidth == innerWidth` |
| 7 | P3 одно состояние под тремя именами (`web/app.js:299`) | Ключ `srv.notProbed` удалён; `badgeState` при неизвестном health отдаёт `srv.unchecked` | Везде «not checked» / «без проверки»: шапка набора «10 healthy · 1 failed · 0 not checked», бейджи на #/servers, `docs/en/panel.md:34` |
| 8 | P3 `TOOLS —` при шапке `TOOLS 117` (`web/app.js:1635`) | Запущенный непроверенный сервер — «?» с подсказкой `setSrv.toolsTip`, остановленный — «—» (колонки нет без набора) | #/set/main: все 11 строк «?», шапка «TOOLS 117»; после Stop — «—» |

Побочно: в `act(btn, path, done)` подпись откатывалась к старой после
перерисовки (`web/app.js` ~3570) — теперь возвращается к актуальной;
проверено Stop→«Start», Start→«Stop».

## Найдено при проверке ворот

Восемь пунктов ворота не покрывали, поэтому всплыло ещё три дефекта — все
исправлены:

- **RU-переполнение на 1280.** Колонка действий при 1280 в RU вылезала за
  край ещё до правок: замер на слепке «до» дал `scrollWidth` 1319 при 1280.
  Аудит этого не ловил — мерили только EN и только 375. От правки 6 стало
  1327 (крестик вырос до 32 px) → `.cell-actions { white-space: normal }`:
  кнопки переносятся, страница не скроллится, в RU строки таблицы
  двухстрочные (высота 86 px).
- **RU-переполнение на 375 в `#/vars`.** Строка переменной давала 407 при
  375: длинная подпись хранилища («set · openbao · …») выталкивала крестик
  за край → `.varlist .v-state { flex: 0 1 auto; min-width: 0 }`, подпись
  переносится.
- **Чип пользователя в топбаре.** `SPAN.who` (`web/index.html:161`,
  `web/app.css:281`) вылезал на 1.3 px при 375 (`right=376.3`) — было и до
  правок. На телефоне чип статичный (`title="admin"`, без действия), поэтому
  в медиазапросе ≤640 (`web/app.css:842-843`) он скрыт целиком вместо
  обрезки подписи.

## За полосой прокси

- **Русские строки бэкенда в ответах API:** `internal/mcpserver/resources.go:157`
  «неизвестный сервер %q» и родственные. Панель покажет их как есть — при
  EN-интерфейсе это снова будет русский текст, но уже из прокси, не из `web/`.
- **`PUT /api/sets/<id>` и `POST /api/sets` в демо** по-прежнему молча
  отвечают `{ok:true}`: в восемь пунктов не входили, поэтому не трогал.
  В демо-режиме они так же «ничего не делают», как раньше кнопки набора.

## Ворота

- `node --check web/app.js` — OK.
- `go build ./...` — OK (сначала падало на чужой правке
  `cmd/mcprig/status.go:75:41: c.panelCreds undefined (type *statusConfig has no field or method panelCreds)`;
  смежная полоса её дозакрыла).
- `bash scripts/docs-check.sh` — 5/5 ok.
- `scrollWidth == innerWidth` на 1280 и 375, EN и RU, для `#/sets`,
  `#/set/main`, `#/servers`, `#/vars`, `#/stats/today`.
- Консоль: 0 ошибок и 0 предупреждений за всю сессию проверок.
- win98: радиусы `0px`, тени `none`, градиенты `none` у `.set-item`, `.btn`,
  `.btn-xs`, `.badge`, `.badge-ok`, `.badge-err`, `.input`, `.table`, `.rail`,
  `.topbar`, `.tab`, `.drawer`, `dialog`, `.statusbar`, `.kpi`.
- Паритет ключей интерфейса: 354 в EN, 354 в RU, расхождений нет.
- Скриншоты до/после — `design/ui/panel-fixes/` (`before-servers`,
  `before-calls` — слепок «до» на 9194; `after-servers`,
  `after-set-overview`, `after-calls`, `after-mobile-sets`).

## Ограничения

- Всё проверено на демо-данных (`?mock=1`). Живой бэкенд не поднимался: пустой
  реестр отказывается стартовать («у набора нет transport — задайте stdio или
  http»), а боевой конфиг поднял бы второй инстанс на портах 9100–9104.
  `?mock=1` идёт через тот же `web/embed.go`, что и боевая панель, но ответы
  бэкенда при этом подменены.
- Клавиатурная навигация по вкладкам (Home/End), нативные `window.confirm`
  и file input — это P4/P5 из аудита, в восемь пунктов не входили и оставлены
  как есть.
