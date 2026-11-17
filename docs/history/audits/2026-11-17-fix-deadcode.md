# Полоса «мёртвый код и дубли» (Go): что сделано (2026-11-17)

Этап 3 аудита репозитория mcprig 0.6.3. Основание —
[аудит сопровождаемости 2026-11-16](2026-11-16-maintainability.md), пункты M1–M3 и
долг D12 из [codemap-дрейфа 2026-11-16](2026-11-16-codemap-drift.md). Правился только
Go-код `cmd/`, `internal/`; `web/`, `docs/` (кроме этого файла), `deploy/` — чужие полосы.

## 1. Мёртвый код (M3) — удалено

`deadcode ./...` молчит: до правки было ровно четыре находки, все закрыты удалением.

| функция | файл | что удалено вместе с ней |
| --- | --- | --- |
| `newTokenSet` | `internal/httplisten/httplisten.go:77` | вызов в `httplisten_test.go:409` переведён на продовый `newAuthSet(...).tokens` |
| `hashPassword` | `internal/manager/auth.go:83` | фикстура вместо генератора: `pwHash` (`internal/manager/scrypt_test.go:38`) — литерал `scrypt$16384$8$1$…` для пароля `pw`; тесты `TestPasswordHash` и `TestLoginQueueLimitsScrypt` берут его |
| `Callback.URL` | `internal/oauth/callback.go:91` | тесты (package `oauth`) читают поле `cb.url` напрямую — пакет тот же, доступ не изменился |
| `QueryHistory` | `internal/stats/history_query.go:362` | 10 вызовов в `internal/stats/history_test.go` и `history_stream_test.go` переведены на `QuerySets([]Source{{Dir: dir}}, q, now)` — это и есть продовый путь |

Решение по `hashPassword`: тест формата пароля (`scrypt$N$r$p$соль$хеш`) — единственное,
что удерживало функцию живой; чтобы не потерять покрытие `parseScryptHash`/`verify`,
оставлен фиксированный хэш, а не удалён весь тест. Литерал сгенерирован
`hashlib.scrypt(n=16384, r=8, p=1, dklen=32)` и проверен разбором в тесте.

## 2. Дубли флагов панели (M1) — сведено

Новый `panelFlags(fs *flag.FlagSet, panel *string, helpPanel string, creds *panelCreds)`
в `cmd/mcprig/panel.go` рядом с `panelCreds`; четыре команды зовут его:
`approve.go:33`, `logs.go:50`, `status.go:73`, `warm.go:69`.
Три конфига (`approveConfig`, `logsConfig`, `statusConfig`, `warmConfig`) вместо полей
`Password`/`Secret`/`User` встраивают `panelCreds`, а `creds := panelCreds{Password: o.Password, …}`
в четырёх `run*` стал `creds := o.panelCreds`.

`helpPanel` — параметр, а не константа, потому что у `warm` подсказка `--panel` своя
(«…для --all и для резолва порта по имени набора…»); остальные три получают `panelHelp`.

**Проверка «то же самое»:** до правки собран `.tmp/mcprig-before`, после — `.tmp/mcprig-after`;
`mcprig {approve,logs,status,warm} --help` дампились в файлы, `diff -r` — **HELP IDENTICAL**
(flag сортирует флаги по имени, поэтому порядок регистрации на вывод не влияет; имена,
help-текст и env-фолбэки не менялись — они в `docs/en/cli.md` и `docs/ru/cli.md`).

## 3. Хелперы-близнецы (M2) — сведено

- **`toolText` ≡ `firstText`** (а не «нормализация URL/адреса», как было в постановке —
  в аудите M2 этой парой названо извлечение первого текстового блока).
  `firstText` в `internal/manager/health.go` стал `FirstText` (используется также в
  `prompt.go:342,400`), `toolText` удалён из `cmd/mcprig/warm.go`, четыре вызова —
  `manager.FirstText(res)`. Семантика (`nil` и пустая строка → `""`) не менялась.
- **`loopbackHost` ≡ `loopbackAddr`**: `loopbackAddr` в `internal/manager/runtime.go`
  стал `LoopbackAddr`; `loopbackHost` удалён из `cmd/mcprig/doctor.go`, два вызова
  (`doctor.go:276`, `:517`) — `manager.LoopbackAddr(...)`. Таблица переходов та же:
  `""`/`"0.0.0.0"`→`127.0.0.1`, `"::"`/`"[::]"`→`::1`.
- **Плюрализация**: `problemWord`/`keyWord`/`variableWord`/`fileWord` удалены из
  `cmd/mcprig/doctor.go`, все пять мест зовут `pluralBy(n, one, few, many)` из
  `warm.go:435`. Русские словоформы (на них ссылаются `docs/*/cli.md`) переданы дословно:
  `проблема/проблемы/проблем`, `ключ/ключа/ключей`, `переменная/переменные/переменных`,
  `файл/файла/файлов`.

## 4. Дубль констант здоровья (D12) — один источник

Новый пакет `internal/healthconst/healthconst.go` (15 строк): `TTL = 15s`,
`Timeout = 15s` с объяснением, почему 15 (холодный stdio npx/uvx и первый коннект к LSP).

Пакет отдельный, а не `internal/manager`: `internal/manager` в тестах импортирует
`internal/mcpserver` (`health_service_test.go`), поэтому обратный импорт дал бы
`import cycle not allowed in test`.

- `internal/mcpserver/manager.go`: локальные `healthTTL`/`healthTimeout` удалены,
  используются `healthconst.TTL` (`manager.go:831`) и `healthconst.Timeout` (`manager.go:855`).
- `internal/manager/health.go`: `healthTTL = healthconst.TTL`, `healthTimeout = healthconst.Timeout`
  (алиасы, `healthLoadTimeout = 2 * healthTimeout` не изменился).
- `internal/mcpserver/healthconst_test.go` (37 строк, сверял числа регуляркой по
  исходнику соседа) удалён: сверять больше нечего — одно объявление.
- `internal/mcpserver/codemap.md:36-37,124` — строки про `healthTTL`/«Coupling to watch»
  переписаны на `healthconst`; заодно поправлены сдвинувшиеся `maxProbes`/`probeSlots`.

## 5. Ворота (вывод)

```
$ go build ./...
build=0                     # вывода нет

$ go vet ./...
vet=0                       # вывода нет

$ ~/go/bin/deadcode ./...
deadcode=0                  # пусто

$ ~/go/bin/golangci-lint run --timeout=10m
0 issues.
lint=0

$ go test ./... -race -count=1 -p 1
Go test: 696 passed in 17 packages
```

Промежуточный прогон затронутых пакетов (`httplisten`, `oauth`, `stats`, `manager`,
`mcpserver`, `cmd/mcprig`, `-race -p 1`) — `538 passed in 6 packages`.

## 6. Документация

- `CHANGELOG.md` — строки **нет**: поведение не изменилось ни для CLI (`--help`
  побайтово тот же), ни для API (`QueryHistory` не был достижим из продового кода),
  ни для панели (числа здоровья те же).
- `docs/history/index.md` — не трогал (по постановке).
- `codemap.md` корня и `internal/healthconst/codemap.md` не заведены: корневой атлас —
  чужой полосе (codemap-дрейф), новый пакет стоит внести туда одной строкой;
  `.slim/codemap.json` не обновлялся.

## 7. Что осталось за рамками

- `internal/oauth/handler.go:25` `Handler.fetch` (поле, читается только тестом) и
  `Handler.Addr`, `Store.Delete` — это M3/D11 других полос, здесь не трогались.
- `deadcode` в `-test`-режиме не гонялся: задача закрывала продовый набор.
- Файл `internal/mcpserver/manager.go` правит параллельно другая полоса — в
  `git diff` по нему видны и чужие строки; правка этой полосы там только const-блок
  и два использования (`healthconst.TTL`/`Timeout`).
