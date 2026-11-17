# Документация после ревью и двух полос — 2026-11-17

Дата: 2026-11-17. Источник задач: список оркестратора, сверенный по коду после
правок полос `internal/mcpserver` и менеджера/панели.

Границы: правились только `docs/en/` и `docs/ru/` (`meta-tools.md`, `panel.md`,
`upgrading.md`), плюс этот отчёт и строка в `docs/history/index.md`.
`CHANGELOG.md`, `docs/{en,ru}/cli.md`, `cmd/`, `internal/*.go` и `web/` не
тронуты: `cli.md` — другая полоса, `CHANGELOG.md` сводит оркестратор.

Факты сверены с кодом, каждый `file:line` в тексте документации проверен по
дереву:

- `internal/mcpserver/invokemany.go:82-92` — дедлайн веера создаётся только при
  `requestTimeout > 0`; `:103` `limit := m.callTimeoutMs(c.TimeoutMs)`,
  `:104` `timeout_ms`, `:127` `callCtxFor(ctx, limit)`.
- `internal/mcpserver/manager.go:1354-1362` `identityOf` (отпечаток ключа,
  иначе адрес клиента, иначе `stdin`); `internal/mcpserver/ratelimit.go:53`
  `mayRefresh` ключуется им же.
- `internal/mcpserver/hints.go` — таблица подсказок без `snyk`,
  `mermaid-chart`, `scalar`; правило шапки файла — «что решает и с чего
  начать».
- `internal/mcpserver/manager.go:1191-1200` — слот у одиночного вызова,
  отказ `нет свободного слота для %s/%s`; те же слоты, что у `invoke_many`
  (`internal/mcpserver/invokemany.go:19-21`, `maxPerServer = 4`).
- `internal/mcpserver/toolout.go:124-151` `truncateJSON` и `:160`
  `stringFloor = 32`; сырой срез — `internal/mcpserver/toolout.go:93-104`.
- `web/app.js:949-955` `reloadReason`, `:1779-1789` `saveSetServers`,
  `:2867` строка переменных; `internal/manager/api.go:1085-1114`
  (`reloadErrors` у `PUT`/`DELETE /api/env/<NAME>`) и `:1485-1505`
  (`failed` / `reloadError` у `PUT /api/sets/<id>`).
- `internal/registry/types.go:105-108` (`PublicKey.Label` + `Note`),
  `internal/registry/store.go:586` `key.Note = key.Label`, `:658`
  `keys[idx].Note = note`, `internal/registry/keys.go:109` перенос
  `note` → `label` при загрузке, `web/app.js:3696-3697` и `:4186-4197` —
  панель шлёт `label`.

## Правки

1. **Бюджет веера `invoke_many`.** `docs/en/meta-tools.md:398-407` и
   `docs/ru/meta-tools.md:397-404`: бюджет назван существующим только при
   заданном `requestTimeout`; без него предела у веера нет и предел соседа не
   достаёт вызов без своего `timeoutMs`. Там же сказано, что тот же предел на
   сервер действует у одиночного `invoke_command`.
2. **Окно `refresh: true`.** `docs/en/meta-tools.md:105-111` и
   `docs/ru/meta-tools.md:105-113`: «не чаще раза в 5 с на вызывающего и
   сервер», с той же идентичностью, что у лимита частоты (отпечаток ключа, у
   открытого набора — адрес клиента, за прокси общий).
3. **Подсказки `hint`.** Правок не потребовалось: имена серверов таблицы
   подсказок в `docs/en/` и `docs/ru/` не перечисляются — `meta-tools.md:60` и
   `configuration.md:67` говорят про «встроенную таблицу» без списка.
4. **Слоты у `invoke_command`.** Новый абзац в `docs/en/meta-tools.md:215-225`
   и `docs/ru/meta-tools.md:216-226`: четыре одновременных вызова на сервер,
   ожидание слота идёт в счёт своего предела, текст отказа
   `Error: нет свободного слота для serena/find_symbol: context deadline exceeded`.
5. **Усечение JSON без длинных строк.** `docs/en/meta-tools.md:326-331` и
   `docs/ru/meta-tools.md:327-332`: валидный JSON обещан, пока в ответе есть
   строки длиннее порога в 32 байта (или массив, у которого отрезается хвост);
   иначе — сырой срез с маркером и фрагмент вместо документа. Список причин
   сырого среза ниже дополнен этим случаем.
6. **Предупреждения в панели.** `docs/en/panel.md:152-155` и
   `docs/ru/panel.md:211-215` (Variables): `reloadErrors` дают строку
   `<набор>: причина` под формой. `docs/en/panel.md:166-169` и
   `docs/ru/panel.md:143-146` (Set composition): `failed` или `reloadError`
   дают ту же строку, потому что правка записана, а набор её не получил.
7. **`note` → `label`.** Новый раздел `docs/en/upgrading.md:42-49` и
   `docs/ru/upgrading.md:42-48`: выдача ключей пишет оба поля, старое имя
   принимается, на существующей установке делать ничего не нужно. Раздел
   `panel.md` про ключи (`docs/en/panel.md:123`, `docs/ru/panel.md:126-127`)
   это уже описывал — не дублировалось.

## Ворота

```
$ bash scripts/docs-check.sh
ok   links resolve
ok   no personal paths
ok   no orphans
ok   language parity
ok   anchors
```

Прогон после последней правки — тот же вывод.
