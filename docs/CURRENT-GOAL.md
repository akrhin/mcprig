# Текущий этап

**Этап 1: ядро прокси**
**Статус:** в работе
**Дата:** 2026-11-08

## Этап 0 принят (2026-11-08)

Контрактный e2e прогнан против живого TS-эталона (`127.0.0.1:9091`, seed-набор `demo`):
14 проверок прошли, 3 дефекта эталона воспроизведены исполнением, 1 проверка
ждёт auth-режим.

```
E2E_BASE=http://127.0.0.1:9091 E2E_STRICT=0 go test ./e2e -v   → exit 0
EXPECTED FAIL: GET /api/sets возвращает сырые 48-hex токены
EXPECTED FAIL: POST /api/servers принимает env:{"A":1} → 201 (валидация только «объект»)
EXPECTED FAIL: POST /api/sets с port 9200 → 201 (валидация 1024–65535 вместо 9100–9199)
SKIP: TestAPIRequiresAuth (нужен E2E_AUTH=1 и менеджер с паролем)
```

Третий дефект тянет за собой четвёртый: числовое значение в `env` ломает
`GET /api/env` (`text.matchAll is not a function`), проверено вручную — на чистом
реестре 200, после одного «отравленного» сервера 400.

## Цель этапа 1

Прокси, который читает `servers.json` и отдаёт четыре мета-тула.

## Ворота

```bash
go build ./... && go vet ./... && go test ./...
E2E_BASE=http://127.0.0.1:<go-прокси> go test ./e2e -v      # strict, без E2E_STRICT=0
```

## Сделано

- `internal/config` — формат lazy-mcp `servers.json`, обе формы `command`,
  soft-fail `_configError`, `Usable()`. 3 теста.
- `internal/downstream` — `Connect` на go-sdk: stdio через `CommandTransport`,
  remote через `StreamableClientTransport` с заголовками. 3 теста.
- 9 тестов зелено, `go vet` чист.

## Дальше

1. `internal/mcpserver` — агрегация в `list_servers` / `list_commands` /
   `describe_commands` / `invoke_command`, ленивый коннект, single-flight.
2. Permission-gate (`resolveAction` → отказ вместо вызова).
3. HTTP-listener прокси: bearer, `Host`/`Origin`, лимит тела.
4. Прогон strict-e2e против Go-прокси.

## Проверяет человек

- Набор `demo` из `config/registry.demo.json` поднимается Go-бинарником и
  отвечает `tools/list` через `curl`.
