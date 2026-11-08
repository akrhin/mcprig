# Текущий этап

**Этап 4: менеджер — супервизия наборов и HTTP API**
**Статус:** в работе
**Дата:** 2026-11-08

## Принято

**Этап 0 — контракт.** `e2e/contract_test.go` против живого TS-эталона
(`127.0.0.1:9091`): 14 прошли, 3 дефекта воспроизведены исполнением
(сырые токены в `GET /api/sets`; `env:{"A":1}` → 201; `port:9200` → 201),
1 ждёт auth-режим. Побочно: числовое значение в `env` валит `GET /api/env`
(`text.matchAll is not a function`).

**Этап 1 — ядро прокси.** `internal/config`, `internal/downstream`,
`internal/mcpserver`, `internal/registry`. 43 юнит-теста. Ворота — настоящий
бинарник и настоящий stdio-downstream:

```bash
go test ./e2e -run TestProxyServesMetaTools -v   # PASS 2.2s
```

Проверено: `list_servers` показывает фикстуру живой, `invoke_command echo`
возвращает текст, ошибка downstream (`fixture failure`) доходит до клиента,
неизвестный сервер даёт внятную ошибку.

**Этап 3 — HTTP-транспорт.** `internal/httplisten`: stateless `2026-07-28` тем же
обработчиком, что legacy `initialize` 2025-06-18; bearer fail-closed; `Host` и
`Origin` по `allowedHosts`; лимит 4 МиБ; `/healthz` публичный; 9 тестов, включая
`-race`.

## Этап 2 — сделано, ждёт ворот

`internal/oauth`: хранилище токенов (`sha256(name\0url)` → 32 hex, файл 0600,
атомарная запись), loopback-приёмник кода (`DefaultCallbackAddr` 8947, один
поток авторизации, `ExpectState`, таймаут 5 мин), фабрика
`auth.AuthorizationCodeHandler` (DCR либо предрегистрация, `RequestRefreshToken`,
`persistingSource`). Подключено к `downstream.Connector`: 401/403 запускают
авторизацию, токен переиспользуется после рестарта, обновляется по refresh.
16 тестов пакета под `-race`. Ворота — сквозной прогон против фейкового AS
(discovery → DCR → authorize → token → refresh) в `internal/downstream`.

## Цель этапа 4

Менеджер: супервизия наборов (`internal/manager/runtime.go`), материализация
`servers.json` набора, цепочка секретов (OpenBao → `env.json` → env процесса),
HTTP API по `docs/reference/ts-manager-contract.md`, auth fail-closed.

## Ворота этапа 4

`go test ./... -race` плюс сквозной сценарий: создать набор через API, он
стартует на 9100–9199, `list_servers` по его адресу отвечает, `stop` гасит
процесс, реестр переживает рестарт менеджера.

## Дальше

1. Этап 5: панель по макету `design/ui/` (внешние `app.css`/`app.js`, без
   inline-JS) и `mcprig init --client …`.
2. Этап 6: юнит, образ, goreleaser.
3. Ревью: арбитр и janitor по этапам 1–3 (запущены), паладин — перед сдачей.

## Проверяет человек

- `mcprig` поднимает набор с remote-сервером, требующим OAuth, и после
  авторизации инструменты доступны без ручных токенов.
