# CodeQL: закрытие алертов по коду, 17.11.2026

Работа по разбору `docs/history/audits/2026-11-17-code-scanning.md`: закрыть те
алерты, которые чинятся кодом, и явно оставить те, что не чинятся по замыслу.
Ветка `fix/codeql-alerts` от `master` (0.6.4).

| ID | алерты | что сделано | где |
| --- | --- | --- | --- |
| CS1 | `go/path-injection` ×2 | сегмент проверяется по форме до `filepath.Join` | `internal/safepath`, `internal/manager/stats.go:83`, `internal/stats/history_query.go:439` |
| CS2 | `go/path-injection` ×1 | путь монтирования чистится и проверяется | `internal/sandbox/sandbox.go:171-176` |
| CS3 | `go/request-forgery` ×1 | схема, хост и `user:pass@` проверяются перед запросом | `internal/manager/notify.go` |
| CS4 | `go/log-injection` ×30 | один санитайзер на все значения из запроса и конфига | `internal/safelog` |
| CS5 | `js/unused-local-variable` ×1 | **не здесь**: `web/app.js` правит другая полоса | — |
| CS6 | 72 алерта в `docs/diagrams/*.html`, 1 в `design/ui/index.html` | сгенерированные страницы и макеты вне анализа | `.github/codeql/codeql-config.yml` |
| CS7 | — | **не код**: secret scanning включается в настройках GitHub | — |

## CS1. Сегмент пути

Правило имени (`^[a-z0-9][a-z0-9_-]{0,63}$`) жило одним `var namePattern` в
`internal/registry/validate.go:15` и применялось только внутри реестра. Теперь
это `internal/safepath.Name` (`internal/safepath/safepath.go:20`), и его зовут:

- `internal/registry/validate.go:142` (имя сервера), `:367` (id набора после
  slugify), `internal/registry/store.go:132` (загрузка `registry.json`);
- `internal/manager/stats.go:83` — `statsDir`: при недопустимом id возвращает
  пустую строку и пишет `Warn` «stats: имя набора не годится в сегмент пути».
  Вызывающие терпимы к пустому пути (`dirExists("")` → false, `GET /api/stats`
  отдаёт пустые данные, а не чужой каталог);
- `internal/stats/history_query.go:439` — `cached` при пути, не прошедшем
  проверку, возвращает `nil`: читателю истории это «файла нет», и он уже умеет
  с этим жить. Проверка стоит до `os.Stat`, то есть до первого касания ФС.

**Почему `internal/safepath`, а не реестр.** Правило нужно трём пакетам сразу:
`registry` (имена и id), `manager` (каталог статистики набора) и `stats` (пути,
собранные из каталога набора). `stats` не может импортировать `registry` —
реестр импортирует всё, что описывает набор, а `manager` импортирует `registry`;
любое из этих мест дало бы цикл. Пакет ниже всех трёх, зависимостей у него нет
вовсе, так что цикл не возникнет ни при каком порядке импортов — тот же приём,
что у `internal/healthconst`.

Заодно в пакете появился `safepath.Path` (`:25`): абсолютный, без элемента `..`,
без управляющих символов. Им закрыты CS2 и `cached` — там путь приходит не
сегментом, а целой строкой.

## CS2. Путь монтирования песочницы

`bind` в `bwrapArgv` теперь сначала делает `filepath.Clean(src)` и только потом
проверяет `safepath.Path(src)`; ошибка — `sandbox: путь %q должен быть
абсолютным и без «..»`, в стиле соседних сообщений. Порядок важен: до правки
bwrap получал путь как записан в конфиге, а теперь — ровно тот, который
проверили. Заданный в `ro`/`rw` путь с `..`, который после чистки остался
относительным (`../etc`), отвергается при старте сервера, как и раньше
отвергался относительный.

## CS3. Вебхук уведомлений

`notifier.post` разбирает адрес через `url.Parse`, ошибку отдаёт через
`redactURL` (как и остальные ошибки отправки), затем требует `http`/`https`,
непустой хост и отсутствие `user:pass@`. Запрос строится из разобранного `u`,
а не из исходной строки. Проверка при записи (`registry.Notify.Check`) осталась:
она про конфиг и отдаёт 400, а новая — про отправку, и она же закрывает случай
`registry.json`, поправленного руками.

## CS4. Значения в логе

`internal/safelog.Value` (`internal/safelog/safelog.go:12`) вырезает
управляющие символы (`unicode.IsControl`: `\r`, `\n`, `\t`, остальные C0/C1),
оставляя текст как есть. Применён к 42 значениям в тех восьми файлах, что
названы в разборе (30 сообщений): `internal/manager/{api,runtime,setconfig,
notify,stats}.go`, `internal/oauth/handler.go`, `internal/httplisten/
httplisten.go`, `internal/downstream/connect.go`. Формат сообщений и набор
полей не менялись — только значения; где в `slog` шёл срез (`hosts`,
`loopbackHosts`), он и остался срезом.

Имена наборов и серверов уже прошли `safepath.Name`, то есть управляющих
символов в них быть не может; `safelog.Value` стоит и там — чтобы место записи
не зависело от того, какая проверка отработала раньше.

## CS6. Сгенерированные страницы вне анализа

`.github/codeql/codeql-config.yml`:

```yaml
paths-ignore:
  - docs/diagrams/**
  - design/ui/**
```

`docs/diagrams/` — выгрузка archify (правится только `.json`, `.html`/`.png`
генерируются), `design/ui/` — макеты-исследования. Оба — не продукт. Подключён
в `.github/workflows/codeql.yml` через `config-file:` у
`github/codeql-action/init`; `languages` и `queries: security-and-quality`
остались как были, прочие пути анализируются по-прежнему. Шире исключение не
делал: исключение, покрывающее больше находки, прячет дефекты, а не шум.

## Что осталось и почему

- **CS5** (`js/unused-local-variable`, `web/app.js:911` — `function uptime(set)`,
  вызовов нет) — правится владельцем: файл в зоне другой полосы.
- **CS7** — secret scanning включается в настройках репозитория, не кодом.
- **`go/command-injection`** `internal/downstream/connect.go:165` — запуск
  MCP-сервера из конфига и есть суть прокси.
- **`go/cookie-secure-not-set`** `internal/manager/auth.go:490,506` — `Secure`
  выставляется по факту TLS; литерал `true` убил бы вход на http-панели.
- **`js/client-side-request-forgery`** `web/app.js:1031` — ложное: адрес собран
  из констант панели.
- Если CodeQL после PR всё ещё покажет часть `go/log-injection` на уже
  санированных значениях (поток через промежуточные структуры анализатор порой
  не прослеживает), разбирать это надо в PR по конкретному алерту, а не
  добавлять проверки, которые код читает хуже.

## Тесты

- `internal/safepath/safepath_test.go` — `TestName` (пустой, `..`, `.`, `a/b`,
  `/etc`, `Set`, `set.`, `-x`, пробел, `\x00`, 65 символов — отказ; `a`, `set1`,
  `my-set`, `a_b`, 64 символа — проходят), `TestPath` (относительный, `..`,
  `\x00` — отказ).
- `internal/safelog/safelog_test.go` — `TestValue`: `\r`, `\n`, `\t` вырезаны,
  чистый текст не изменён.
- `internal/sandbox/sandbox_test.go` — `TestWrapDotDotPath`: `../etc` и
  `../../etc/passwd` отвергаются, путь `dir/sub/..` монтируется как `dir`.
  Через `filepath.Join` этот тест не построить — `Join` чистит сам.
- `internal/manager/notify_test.go` — `TestNotifyPostRejectsUnsafeWebhook`:
  `file:///etc/passwd`, `http://user:pass@example.com/hook`, `ftp://…`,
  `http:///nohost`, `://bad` — все отбиты.

## Ворота

```text
go build ./...                          -> чисто
go vet ./...                            -> чисто
~/go/bin/golangci-lint run --timeout=10m -> 0 issues.
~/go/bin/deadcode ./...                 -> пусто
go test ./... -race -count=1 -p 1       -> EXIT=0, 710 passed, 19 packages
bash scripts/docs-check.sh              -> links, no personal paths, no orphans, language parity, anchors (10) — ok
```

`node --check web/app.js` в этой полосе не гонялся: файл не наш.

## Строки для CHANGELOG (вписывает владелец)

`CHANGELOG.md`, под `## [Unreleased]`:

```markdown
- Security: set names used as a path segment are checked before the join, sandbox
  mount paths are cleaned and refused when they still climb out, and the notify
  webhook is re-checked (http/https, host, no `user:pass@`) right before sending.
- Security: log values from the request and the config pass through one sanitiser
  that strips control characters, so a name cannot forge a journal line.
- CI: CodeQL no longer scans generated pages (`docs/diagrams/`) and UI mockups
  (`design/ui/`).
```

`CHANGELOG.ru.md`, под `## [Unreleased]`:

```markdown
- Безопасность: имя набора как сегмент пути проверяется до склейки, путь
  монтирования песочницы чистится и отвергается, если всё равно выходит наверх,
  а вебхук уведомлений перепроверяется (http/https, хост, без `user:pass@`)
  прямо перед отправкой.
- Безопасность: значения из запроса и конфига идут в лог через один санитайзер,
  который вырезает управляющие символы, — именем нельзя подделать строку журнала.
- CI: CodeQL больше не анализирует сгенерированные страницы (`docs/diagrams/`) и
  макеты интерфейса (`design/ui/`).
```
