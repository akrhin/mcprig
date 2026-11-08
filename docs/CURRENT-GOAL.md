# Текущий этап

**Этап 5: панель и дашборд**
**Статус:** в работе
**Дата:** 2026-11-08

## Принято

**Этап 0 — контракт.** `e2e/contract_test.go` против живого TS-эталона
(`127.0.0.1:9091`) и против нашего `panel`: 11 контрактных тестов, в strict —
зелёные на обоих. Дефекты эталона воспроизведены исполнением и у нас закрыты:
сырые токены в `GET /api/sets`, `env:{"A":1}` → 201, `port:9200` → 201.

**Этап 1 — ядро прокси.** `internal/config`, `internal/downstream`,
`internal/mcpserver`, `internal/registry`. Ворота — настоящий бинарник и
настоящий stdio-downstream (`e2e/proxy_test.go`).

**Этап 2 — OAuth.** `internal/oauth` (хранилище токенов, loopback-приёмник,
фабрика `auth.AuthorizationCodeHandler`, `persistingSource`) подключён к
`downstream.Connector`. Сквозной тест против фейкового AS: первый вход — браузер
+ DCR, второй — без браузера, третий — refresh по истечении.

**Этап 3 — HTTP-транспорт.** `internal/httplisten`: stateless `2026-07-28` тем
же обработчиком, что legacy 2025-06-18; bearer fail-closed (кроме открытых
наборов через `allowAnonymous`); `Host`/`Origin` по `allowedHosts`
(пустой список = loopback); лимит 4 МиБ; `/healthz` публичный.

**Этап 4 — менеджер.** `internal/manager`: супервизия набора (spawn без shell,
готовность по TCP, рестарт с backoff, SIGTERM → SIGKILL, лог с ротацией),
материализация `servers.json`, цепочка секретов OpenBao → `env.json` → env
процесса, auth (none/proxy/password/оба, scrypt, cookie с HMAC), HTTP API по
контракту, `internal/atomicfile` для всех записей.

Проверено вживую: `panel` → создать сервер и набор → `start` → набор на 9100 →
`invoke_command echo` возвращает «привет» → `/stats` показывает вызовы по
клиентам (`opencode/2.2.6` и `hermes/1.2` — раздельно) → `stop` гасит процесс.
Открытый набор (`"open": true`) стартует без ключа.

## Ворота (зелёные на `cecd6d3`)

```bash
go build ./... && go vet ./... && go test ./... -race -count=1   # 121 тест, 12 пакетов
E2E_BASE=http://127.0.0.1:9099 E2E_STRICT=1 ./scripts/contract-e2e.sh
```

## Цель этапа 5

Панель по макету `design/ui/`: страница из внешних `app.css`/`app.js` (без
inline-JS), список наборов, карточка набора, таблица вызовов, правая колонка
серверов/переменных/лога, три состояния. Данные — HTTP API панели плюс
`GET /stats` набора (счётчик в процессе прокси). Плюс `mcprig init --client
opencode|claude|codex --set <id> [--remote]`.

## Дальше

1. Этап 6: юнит systemd (готов в `deploy/`), образы (готовы, не собирались),
   goreleaser, публикация в npm/PyPI/crates под именем `mcprig`.
2. Ревью: паладин (безопасность) и арбитр (готовность к мержу) по `cecd6d3` —
   запущены, находки разводятся отдельными полосами.

## Проверяет человек

- Открыть панель в браузере: наборы, карточка, вызовы, лог — и сверить, что
  цифры совпадают с тем, что реально дергали.
- Подключить `mcprig stdio` к opencode и Hermes и увидеть оба харнеса в
  счётчике раздельно.
