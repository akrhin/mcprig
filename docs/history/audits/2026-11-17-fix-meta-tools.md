# Полоса 2026-11-17 — `isError`, пер-вызовный `timeoutMs`, `hint` в `list_servers`

Дата: 2026-11-17. База: master `846fae1` (0.6.3) + незакоммиченная волна прошлых
полос. Источник задач: постановка оркестратора (m00001).

Границы: правились `internal/mcpserver/`, `internal/config/`, `internal/registry/`
(только поле `hint`), `CHANGELOG.md` и этот отчёт. `web/` и `docs/{en,ru}` не
тронуты — что там дописать, перечислено в конце. Коммитов нет.

## 1. `isError` при провале тула — код уже был, правка не понадобилась

**Состояние.** Требование выполнено волной прошлой полосы; проверено чтением и
тестами, ничего не менялось.

- `internal/mcpserver/manager.go:1786` `errResult(err)` — единственный конверт
  ошибки: `IsError: true`, текст `"Error: " + err.Error()`. Через него идут все
  отказы `invoke_command` (strictArgs, разбор `parameters`, неизвестный сервер,
  троттлинг, непригодный сервер, `toolForCall`) и ошибки `invoke_many` до запуска
  веера (включая `checkTimeoutMs`, см. п. 2).
- `internal/mcpserver/permissions.go:67` `blockedResult` — `IsError: true`,
  `_meta.lazyMcpPolicy: "blocked"`; `internal/mcpserver/quarantine.go:170`
  `quarantineResult` — `IsError: true`.
- `internal/mcpserver/toolout.go:54` `truncateResult` переносит `IsError` в оба
  варианта обрезки (структурный JSON и сырой байт), поэтому обрезанный отказ
  остаётся отказом.
- `internal/mcpserver/invokemany.go:149` — агрегат: `IsError` ставится, только
  когда провалились **все** вызовы; частичный отказ остаётся данными (в элементах
  `ok:false` + `error`).

**Тесты (существующие, зелёные).** `internal/mcpserver/tools_test.go:21`
`TestInvokeCommandTimeout` (таймаут → `IsError`, текст `"таймаут вызова 20 мс"`,
`slow/wait`); `internal/mcpserver/manager_test.go:245` `TestPermissionDeny`
(отказ политики → `IsError`); `internal/mcpserver/invokemany_test.go:304`
`TestInvokeManyAllFailedIsError`; `invokemany_test.go:482`
`TestInvokeManyDenyKeepsPolicyShape`.

## 2. Пер-вызовный `timeoutMs`

**Было.** Один `requestTimeout` набора (120 с по умолчанию) на любой вызов;
агент, которому нужен более долгий или более короткий предел, изменить его не мог.

**Стало.**

- `internal/mcpserver/manager.go`: `invokeCommandInput.TimeoutMs`
  (`json:"timeoutMs,omitempty"`), границы `minCallTimeoutMs = 1000` /
  `maxCallTimeoutMs = 600000`, `checkTimeoutMs` (`manager.go:1284`) — отказ до
  запуска: `"timeoutMs: допустимо от 1000 до 600000 мс"` (текст русский, как
  остальные отказы); `0` — параметр не задан.
- `callCtxFor(ctx, timeoutMs)` (`manager.go:1449`) — `context.WithTimeout` при
  заданном значении, иначе прежний `callCtx` на `requestTimeout`;
  `callTimeoutMs(requested)` (`manager.go:1458`) — фактический предел (свой
  важнее набора), он же попадает в сообщение
  `"таймаут вызова %d мс: %s/%s не ответил: %w"`.
- `withTimeoutMeta(res, timeoutMs)` (`manager.go:1796`) кладёт фактический предел
  в `_meta.timeoutMs`. Мета ставится **после** `truncateResult`, который
  собирает новый `CallToolResult` и мету не переносит.
- `internal/mcpserver/invokemany.go`: `manyCall.TimeoutMs` (тот же ключ и те же
  границы, элемент принимает `timeoutMs` наравне с `server`/`command_name`/
  `parameters`); проверка в `parseManyCalls` с префиксом `calls[i]:`.
  Предел элемента применяется **после** получения слота — ожидание слота
  по-прежнему ограничено только бюджетом веера, как и раньше.
  Бюджет веера: `max(requestTimeout, max(calls[i].timeoutMs))`, чтобы вызов,
  попросивший больше набора, не срезался раньше своего предела. В ответе:
  `timeout_ms` у каждого элемента и `_meta.timeoutMs` — бюджет веера.
- Описания в схемах `invoke_command`/`invoke_many` называют параметр, диапазон и
  `_meta`.

**Тесты.** Новый файл `internal/mcpserver/meta_test.go`:

- `TestInvokeCommandTimeoutMsApplied` — предел принят, `_meta.timeoutMs` = 1500;
  без параметра `_meta.timeoutMs` = `requestTimeout` набора (60000).
- `TestInvokeCommandTimeoutMsStopsCall` — downstream спит 3 с, `timeoutMs` 1000
  при `requestTimeout` 60000: ответ за < 2.5 с, `IsError`, текст
  `"таймаут вызова 1000 мс"` (то есть применился пер-вызовный предел, а не
  наборный).
- `TestTimeoutMsOutOfRange` — 999 и 600001 отвергнуты обоими инструментами,
  с текстом диапазона и префиксом `calls[0]:` у `invoke_many`.
- `TestInvokeManyTimeoutMsPerCall` — `requestTimeout` 800, у элементов 1500:
  медленный (3 с) падает по своему пределу, быстрый (1 с) успевает — значит
  бюджет веера поднят до 1500; частичный отказ остался данными (`IsError` нет),
  `timeout_ms` = 1500 у обоих, `_meta.timeoutMs` = 1500.

## 3. `hint` — «когда нужен этот сервер»

- Новый `internal/mcpserver/hints.go`: `serverHints` — одна строка на 18
  известных серверов (serena, socraticode, xerj, context7, deepwiki, playwright,
  gitea, portainer-mcp, arcane-mcp, dap-mcp, raven, designer-skill, tavily-mcp,
  open-design, code-review-graph, snyk, mermaid-chart, scalar) и
  `serverHint(srv)` — своя строка из конфига главнее встроенной. Комментарий в
  файле объясняет, зачем таблица: агент видит только имя и описание, а serena,
  socraticode и xerj на вид решают одно и то же.
- `internal/config/config.go`: `Server.Hint string \`json:"hint,omitempty"\``,
  чтение в `UnmarshalJSON`; `internal/registry/validate.go` —
  `optionalString(in, "hint")`; `internal/registry/importexport.go` — экспорт
  `hint`, когда он задан.
- `internal/mcpserver/manager.go`: `serverInfo.Hint` с `omitempty` +
  `Hint: serverHint(srv)` в `listServers`; у незнакомого сервера без своей
  строки поля в JSON нет вовсе (пустая строка читалась бы как «решать нечего»).
  Описание `list_servers` говорит про подсказку.

**Тесты.** `TestListServersHint` в `meta_test.go`: встроенная (serena содержит
`activate_project`), переопределённая (context7 из конфига), и у незнакомого
`acme` ключа `hint` нет в сыром JSON. `serversOut` в `manager_test.go` получил
поле `Hint` (и именованный тип `serversOutServer`).

## Попутно

- `golangci-lint` ловил `G101` на таблице подсказок: gosec ищет `pw` в именах
  ключей, а в `deepwiki` оно есть. Строка помечена
  `//nolint:gosec // G101: «pw» он находит в имени deepwiki — это подсказки, а не секреты.`
- `internal/mcpserver/tools_test.go:64` — вызов `m.callTimeout` получил новый
  аргумент (`0`).

## Ворота

```
go build ./...                                  BUILD_OK
go vet ./...                                    VET_OK
~/go/bin/golangci-lint run --timeout=10m        0 issues.
~/go/bin/deadcode ./...                         (пусто, код 0)
go test ./... -race -count=1 -p 1               Go test: 701 passed in 17 packages
```

## Что дописать другой полосе

1. `docs/en/meta-tools.md` + `docs/ru/meta-tools.md`: таблица параметров
   `invoke_command` (строка с `maxBytes` ~177) — `timeoutMs`, 1000..600000,
   по умолчанию `requestTimeout`, фактический предел в `_meta.timeoutMs`;
   `invoke_many` (~366, ~371) — `timeoutMs` у элемента, `timeout_ms` в ответе,
   бюджет веера в `_meta`, формулировка «one `requestTimeout` bounds the whole
   fan-out» больше неполна; `list_servers` — поле `hint` и правило
   переопределения.
2. `docs/en/configuration.md` + `docs/ru/configuration.md`: в таблицу полей
   сервера (рядом с `description`/`tags`, ~66-75) — `hint`.
3. `codemap.md` каталога `internal/mcpserver` (и корневой атлас, если он
   перечисляет файлы): новый `hints.go`, `callCtxFor` (`manager.go:1449`),
   `callTimeoutMs` (`manager.go:1458`), `checkTimeoutMs` (`manager.go:1284`),
   `withTimeoutMeta` (`manager.go:1796`), изменённая сигнатура `callTimeout`.
   Ссылки `file:line` в `internal/mcpserver/codemap.md` сдвинулись на ~60 строк
   от начала `manager.go` — обновлять через `scripts/codemap.mjs`, а не руками:
   после правки `node …/scripts/codemap.mjs update`.
4. Вне репозитория: `~/.config/opencode/AGENTS.md` в разделе «Полевой тест»
   утверждает «ошибки приходят текстом ответа, а не `isError`; таймаут один на
   набор (120 с), пер-вызовного нет» — после этой полосы неверно.
