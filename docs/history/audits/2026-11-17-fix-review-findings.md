# Полоса 2026-11-17 — четыре находки финального ревью в `internal/`

Дата: 2026-11-17. Источник задач: постановка оркестратора (P2 + три P4).

Границы: правились `internal/mcpserver/` и `internal/registry/`, плюс этот
отчёт и строка в `docs/history/index.md`. `web/` и `docs/{en,ru}` не тронуты —
что там дописать, перечислено в конце. Коммитов нет.

## 1. P2 — бюджет `invoke_many` при `requestTimeout: 0`

**Было.** `budget := m.config().RequestTimeout` (0 — без ограничения), затем
`budget = max(budget, calls[i].TimeoutMs)`, и этот бюджет вешался на родительский
ctx веера (`internal/mcpserver/invokemany.go:80-86`). Вызов без своего
`timeoutMs` получал `callCtxFor(ctx, 0)` — то есть родительский ctx с дедлайном
соседа: в дефолтной конфигурации медленный сосед молча резал его на своём
пределе, а отказ печатал `таймаут вызова 0 мс` и не отдавал `timeout_ms`.

**Стало.**

- Дедлайн веера создаётся только при `requestTimeout > 0`
  (`invokemany.go:82-92`). При `requestTimeout: 0` предела у веера нет: каждый
  вызов либо идёт под своим `timeoutMs`, либо без лимита.
- Предел вызова считается один раз в его горутине
  (`invokemany.go:103` `limit := m.callTimeoutMs(c.TimeoutMs)`) и дальше
  используется везде: `callCtxFor(ctx, limit)` (`:127`), элемент ответа
  `timeout_ms` (`:104`) и текст отказа (`:129`, `runManyCall` получает `limit`
  и передаёт его в `callTimeout`). Поле `manyAnswer.TimeoutMS` теперь всегда
  называет фактический предел, под которым вызов работал.

**Тест.** `internal/mcpserver/invokemany_test.go:347`
`TestInvokeManyWithoutRequestTimeoutKeepsNeighbourOut`: набор с
`requestTimeout: 0`, первый вызов с `timeoutMs: 1000` и `ms: 2000`, второй без
`timeoutMs` и `ms: 1500`. Первый обязан упасть с `таймаут вызова 1000 мс` и
`timeout_ms: 1000`; второй — дойти до конца (`ms > 1000`) с `timeout_ms: 0`
(поле отсутствует). Старый код резал второй на 1000 мс.

## 2. P3 — вернул два потерянных теста усечения

`internal/mcpserver/truncate_test.go:81` `TestUnderLimitAnswerUntouched` —
ответ под лимитом возвращается байт-в-байт и не создаёт файл в `tool-out`
(`truncateResult` при `w.total <= maxBytes` отдаёт результат нетронутым,
`internal/mcpserver/toolout.go:70`).

`internal/mcpserver/truncate_test.go:99` `TestInvokeCommandRejectsOversizeMaxBytes`
— `maxBytes: maxAllowedBytes + 1` у `invoke_command` отвергается с текстом
`допустимо не больше 1048576` (`resolveMaxBytes`,
`internal/mcpserver/manager.go:1261-1274`).

## 3. P4 — подсказки без быстро портящихся фактов

`internal/mcpserver/hints.go`:

- `snyk`, `mermaid-chart` («сейчас не отвечает»), `scalar` («дублирует
  redocly/spectral») удалены: серверов нет в наборе, а утверждения о
  доступности и сравнения серверов протухают. Конфиг может вернуть эти серверы —
  подсказка про прошлое знать не обязана.
- `dap-mcp` переписан без нашей внутренней обёртки:
  «Отладка по брейкпоинтам: breakpoints ставятся до старта сессии, threadId
  обязателен.»
- В шапку таблицы добавлено правило: только устойчивое — что сервер решает и с
  чего начать; доступность и сравнение серверов сюда не пишутся.

## 4. P4 — ключ троттлинга `refresh: true` у открытого набора

Ключ строился из `stats.ClientKey(ctx)`, а у открытого набора он пустой у всех —
второй клиент платил за чужой `refresh: true`. Чинение оказалось строкой: ключ
теперь `identityOf(ctx)` (`internal/mcpserver/ratelimit.go:53`) — та же
идентичность, что у лимитов (`manager.go:1355`): отпечаток ключа, а у открытого
набора адрес (за прокси — адрес прокси, это по-прежнему грубый разделитель, не
защита). Комментарии `catalogRefreshEvery` и `mayRefresh` больше не говорят
«ключ», а говорят «идентичность»; импорт `internal/stats` из файла ушёл.

## 5. P4 — подпись ключа при даунгрейде

`internal/registry/store.go:586` — `IssueKey` пишет и `Label`, и `Note`
(`key.Note = key.Label`). Старый бинарь читает только `note`
(`internal/registry/types.go:37-40`) и раньше видел пустую подпись у ключей,
выданных после обновления. `SetKeyNote` и так писал оба поля
(`store.go:655-657`).

## Ворота полосы

```
go build ./...                                     ok
go vet ./...                                       ok
~/go/bin/golangci-lint run --timeout=10m           0 issues
~/go/bin/deadcode ./...                            пусто (код 0)
go test ./... -race -count=1 -p 1                  705 passed in 17 packages
```

702 прежних + три новых теста (`TestInvokeManyWithoutRequestTimeoutKeepsNeighbourOut`,
`TestUnderLimitAnswerUntouched`, `TestInvokeCommandRejectsOversizeMaxBytes`).

## Что дописать в документации (полоса документации)

- `docs/en/meta-tools.md:377-380` — абзац про бюджет веера: бюджет существует
  только при `requestTimeout > 0`, при нуле общего дедлайна нет, и вызов без
  своего `timeoutMs` не ограничен пределом соседа.
- `docs/ru/meta-tools.md:375-378` — то же в русском зеркале.
- `docs/en/meta-tools.md:107-108` — «at most once every 5 s per key and server»:
  у открытого набора это уже не ключ, а адрес клиента (идентичность та же, что у
  rate limit).
- `docs/ru/meta-tools.md:105-106` — «не чаще раза в 5 с на пару «ключ и сервер»»
  — то же уточнение.
- `docs/en/meta-tools.md:60` / `docs/ru/meta-tools.md:60` — описание поля
  `hint` не изменилось, но набор подсказок теперь без `snyk`, `mermaid-chart`,
  `scalar`; если где-то перечисляются имена серверов таблицы — список сократить.
- `CHANGELOG.md` под `## [Unreleased]` → `### Fixed`: одна строка про
  `invoke_many` при `requestTimeout: 0` (дедлайн соседа больше не режет вызов без
  своего `timeoutMs`, `timeout_ms` называет фактический предел) и одна про
  `refresh: true`, который больше не делит окно между клиентами открытого набора.
