# Текущий этап

**Этап 2: OAuth 2.0 + PKCE для remote-серверов**
**Статус:** не начат
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

## Цель этапа 2

OAuth 2.0 + PKCE для remote-серверов на `auth.NewAuthorizationCodeHandler`.

## Ворота

`httptest`-заглушка сервера авторизации: RFC 8414 discovery → RFC 7591
dynamic client registration → authorize → token → refresh, плюс протухание
токена и повторный запрос.

## Дальше

1. `internal/oauth`: хранение ключей (`sha256(name\0url)`), loopback callback,
   single-flight на обновление.
2. Подключить к `downstream.Connect`: 401 от downstream → пройти авторизацию →
   повторить запрос.
3. Этап 4: супервизор наборов поверх `internal/registry` + lifecycle-e2e.

## Проверяет человек

- `mcprig` поднимает набор с remote-сервером, требующим OAuth, и после
  авторизации инструменты доступны без ручных токенов.
