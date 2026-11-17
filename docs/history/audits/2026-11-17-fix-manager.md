# Fixes: manager error paths, registry, tool-out, OAuth status

Date: 2026-11-17. Base: `master` 0.6.3. Sources: `2026-11-16-project-audit.md`,
`2026-11-16-error-paths.md`. Scope: `internal/manager/api.go`,
`internal/manager/oauth.go`, `internal/manager/*_test.go`,
`internal/registry/store.go` (load only), `internal/toolout/toolout.go` (sweep
only). Nothing committed; `web/` untouched.

Each item below: what was wrong, what it is now, the test that pins it, and how
it was checked. Every test fails on the pre-fix code for the reason named in
"было".

## E-01 — corrupt `registry.json` vanished without a trace

**Было.** `registry.Open` (`internal/registry/store.go:39-68`) renamed an
unparsable file to `registry.json.corrupt-<unix>`, left `s.data` empty and
returned `nil`. The panel came up with an empty registry and the log said
nothing; `Dropped()` only ever carried set ids rejected by the id pattern, so
the fact was invisible everywhere.

**Стало.** After a successful rename the store appends a note to `s.dropped`
(`internal/registry/store.go:48-58`): backup path, unmarshal error, byte size and
the number of directories left under `<data>/sets` — contents of a corrupt file
cannot be counted, directories can. `manager.New` logs every `Dropped()` entry
at ERROR (`internal/manager/api.go:189`, message `реестр: запись отброшена при
загрузке`), so the path to the backup is in the journal next to the empty
registry. No new accessor: the fact is visible through `Dropped()` literally, and
`doctor` already reads and reports a broken `registry.json` on its own.

**Тест.** `TestNewLogsCorruptRegistry` (`internal/manager/manager_test.go`):
garbage `{"sets":[` plus `sets/one` on disk, `New` with a log buffer; asserts an
empty store, exactly one `registry.json.corrupt-*`, and a log line carrying
`level=ERROR`, the backup path and `каталогов наборов на диске 1`.
`TestNewLogsDroppedSets` updated to the new message.

## E-02 — failed restart after an `env` edit only reached the log

**Было.** `apiServerUpdate` (`internal/manager/api.go:1355-1365`) split on
`envChanged`: the env branch called `restartSetsUsing`, which returned only the
ids it touched and wrote failures to `m.log.Warn`; the sibling branch
(`reloadSetsUsing`) returns `failed[set.ID]` and puts it into `reloadErrors`.
Same shape of failure, two different answers: 200 with no mention of the set
that did not come back.

**Стало.** `restartSetsUsing` returns `([]string, map[string]string)` and fills
`failed[set.ID] = err.Error()` (`internal/manager/api.go:2074`); the env branch
assigns it into `reloadErrors` (`:1362`), the response gains `reloadErrors` only
when non-empty (`:1378`). A restart that succeeded is still only listed in
`reloaded`/`restarted`; a set whose restart failed is now named.

**Тест.** `TestServerEnvUpdateReportsFailedRestart`: server `a`, running set on
its real port (`markRunning`), port occupied with a real listener, then
`PUT /api/servers/a` with `env: {"X":"1"}`. Asserts 200, empty `reloaded`, and
`reloadErrors[set]` containing `занят другим процессом`. Pre-fix the field is
absent and the failure exists only as a log line.

## E-03 — secret rotation promised `restarted` while restarting in the background

**Было.** `restartSetsUsingEnv` (`internal/manager/api.go:2009`) collected the
sets, answered `restarted: [ids]`, and restarted them in a goroutine; a failed
restart printed `m.log.Warn` and nothing else. The set kept serving with the old
environment — i.e. with the secret the operator had just rotated away — while
the answer said it had been restarted.

**Решение: ждать рестарт в ответе (синхронно), а не разделять
`restartRequested`/статус.** Причины:

1. `runtime.go` вне моей зоны правки, а ошибку фонового рестарта негде хранить
   так, чтобы её увидели статус набора или `doctor`: после неудачного старта
   процесс не «остановлен с ошибкой», его нет — `status()` берёт `p.err` у
   живого процесса и кольцо последних ошибок, которое заполняется при работе,
   а не при старте. Писать ошибку в реестр значит менять ещё один пакет.
2. Синхронный ответ даёт ту же форму, что E-02: `reloadErrors[setID]`, и
   `restarted` начинает означать «действительно перезапущен», а не «запущен
   в фоне». Оператор видит исход в ответе на собственный запрос.
3. Ограничение по времени уже было: `restartTimeout = 60s` (`api.go:38`).
   Рестарт идёт под `context.WithTimeout(m.ctx, restartTimeout)`; набор,
   которому бюджета не хватило, попадает в `reloadErrors` с причиной
   `перезапуск не дождался: ...`.

**Цена.** `PUT`/`DELETE /api/env/<name>` теперь блокируют запрос на время
реального рестарта (обычно секунды, при больном наборе — до 60 с). Это
осознанный размен: молчаливая ротация ключа дороже ожидания.

**Стало.** `restartSetsUsingEnv(name) ([]string, map[string]string,
[]skippedRestart)`; пропуск по неразрешённым переменным остался прежним;
`apiEnvPut`/`apiEnvDelete` (`:1093`, `:1110`) добавляют `reloadErrors` при
непустой карте.

**Тест.** `TestEnvPutReportsFailedRestart`: сервер со ссылкой
`${DB_PASSWORD}`, работающий набор с занятым портом, `PUT /api/env/DB_PASSWORD`
→ 200, `restarted` пуст, `reloadErrors[set]` содержит `занят другим
процессом`. Pre-fix `restarted` содержит набор (он «перезапускается» в
фоне), а ошибки в ответе нет вообще.

## E-07 — sweep of `tool-out` swallowed delete errors

**Было.** `sweepDir` (`internal/toolout/toolout.go:169-196`) считал удачным
только `os.Remove(e.path) == nil`, иначе молча оставлял файл, и всё равно
возвращал `nil`. Ретенция «работала», файлы не удалялись, диск рос.

**Стало.** Удаление вынесено в замыкание `remove`, которое считает неудачи и
запоминает первую ошибку; неудалённый файл остаётся в `fresh`/`total` (иначе
следующий проход считал бы объём освобождённым), `.owner` убирается только
вместе с выгрузкой; в конце — `fmt.Errorf("не удалось удалить %d файлов: %w",
failed, firstErr)`. Остальные каталоги дочищаются: ошибка агрегируется, а не
прерывает обход. `sweepAndLog` уже логирует любую ошибку `Sweep` на Warn.

**Тест.** `TestSweepReportsRemoveFailure`: каталог набора с истёкшим файлом
под `chmod 0500` (skip под root) и обычный каталог с истёкшим файлом; ждёт
ошибку `не удалось удалить 1`, `Removed == 1`, живой застрявший файл и
удалённый соседний. Pre-fix `err == nil` и `Removed == 1` при двух живых
файлах — тест падает на `err == nil`.

## P3 #39 — `authorize/status` answered `200 idle` for a server outside the set

**Было.** `apiAuthorizeStatus` (`internal/manager/oauth.go`) проверял только
наличие набора и сервера; `apiServerAuthorize` — ещё членство в наборе и
непустой `URL`. Сервер, выброшенный из набора, давал `200 idle`, и диалог
панели крутился вечно.

**Стало.** Общая проверка вынесена в `authorizeTarget(setID, name)
(*config.Server, error)` (`internal/manager/oauth.go:77`): 404 на
несуществующий набор/сервер, 400 `сервер X не входит в набор Y`, 400
`OAuth поддерживается только у удалённого сервера (transport: http)`. Оба
обработчика зовут её; тексты — те же, что были у запуска.

**Тест.** `TestAuthorizeStatusRequiresMembership`: сервер есть, набор
существует, сервер в наборе не значится → `GET .../authorize/status` даёт 400
с `не входит в набор`. Pre-fix — 200 `idle`. `TestAuthorizeIsPostOnly`
по-прежнему ждёт 200 на законную пару.

## Тесты (proctor): R4 и reload остановленного набора

**Было (R4 / #55).** `TestSetUpdateReturnsRestartError`
(`internal/manager/manager_test.go:804-816`) закреплял 500 после
`PUT /api/sets/<id>`, хотя правка к этому моменту уже в реестре и на диске:
клиент считает её потерянной и повторяет запрос.

**Стало.** `apiSetUpdate` (`internal/manager/api.go:1486-1502`) на отказ
применения — и рестарта, и reload — отвечает 200 с `failed[setID]`; тело —
`setView` плюс `failed`. Успешный путь не изменился.

**Тест.** `TestSetUpdateKeepsCommittedEditWhenRestartFails`: набор запущен,
`failRestart` (`rt.closed`), `PUT {"description":"x"}` → 200,
`failed[id]` содержит `менеджер остановлен`, `description == "x"`, и
`registry.json` на диске разбирается с тем же описанием. Pre-fix — 500 (в
этом сценарии правка идёт веткой reload, и `reloadSetView` возвращал
`serverError`).

**Новый тест (#40 — закрыт).** `TestReloadStoppedSetWithoutKey`:
остановленный набор, отозван последний ключ, `POST /api/sets/<id>/reload` →
200, `reloaded:false`, причина называет отсутствующий ключ, `servers.json` на
диске не появляется.

## #40 — reload остановленного набора без ключа отвечал 500

**Было.** `apiSetReload` (`internal/manager/api.go:1565-1578`) на
остановленном наборе собирал конфиг и писал его в `servers.json`, а любую
ошибку сборки отдавал 500. Набор без действующего ключа получал 500 на
операцию, которая ничего не применяет: панель показывала «ошибка сервера» на
нормальном состоянии «остановлен».

**Стало.** Остановленный набор — не отказ: 200 с `reloaded:false` и причиной.
Причина разная — `набор не запущен` (конфиг собран и записан, правка применится
при старте) и `набор не запущен: нет активного ключа — конфигурация не
собрана, выдайте ключ`; во втором случае `servers.json` не пишется вовсе, иначе
старт взял бы половинный конфиг молча. Чтобы отличить «нет ключа» от прочих
отказов без разбора текста, `BuildSetConfig`
(`internal/manager/setconfig.go:62-70`) вернул `ErrNoKey`: текст ошибки не
изменился (`у набора <id> нет активного ключа — выдайте новый`), но теперь
`errors.Is` его узнаёт. 500 остался за настоящими отказами — конфиг не собрался
по другой причине или не записался.

**Почему так.** Reload — идемпотентное «приведи файл в соответствие реестру»;
у остановленного набора применять нечего, поэтому отказом может быть только то,
что помешает следующему старту. Ключ не мешает — он не даёт собрать конфиг, и
это состояние оператор видит в статусе набора и в `doctor`, а не в виде 500 на
кнопку «перечитать».

**Тест.** `TestReloadStoppedSetWithoutKey` — см. выше. Pre-fix ждал 500 и
`ошибка` в теле; на старом коде новый тест падает на `status == 500`.
Существующий `reload_test.go:259-265` продолжает ждать 200 с причиной
`набор не запущен` для набора с ключом — тексты разведены именно поэтому.

## Что не закрыто

- **E-03, видимость в статусе набора** — оператор видит исход в ответе на
  правку, но `GET /api/state` и `doctor` о провалившемся фоновом рестарте не
  узнают. Отдельного поля для этого нет, а класть его в `runtime.go` — вне
  зоны этой полосы. Если фоновая семантика понадобится снова (долгие
  рестарты), нужен слот в `RuntimeStatus` и запись в него из `RestartSet`.
- **`internal/registry` тесты не запускались** — по условию полосы; на них
  параллельно работают соседние пакеты на портах 9100–9199.

## Что дописать в `docs/{en,ru}` (чужая полоса)

- `docs/en/panel.md` / `docs/ru/panel.md`: ответ `PUT /api/servers/<name>`,
  `PUT|DELETE /api/env/<name>`, `PUT /api/sets/<id>` — поле `failed` /
  `reloadErrors` и то, что `restarted` значит «перезапущен», а не «запущен»;
  ротация секрета ждёт рестарт до 60 с. Плюс `POST /api/sets/<id>/reload`:
  остановленный набор отвечает `200 reloaded:false` с причиной, 500 — только
  на настоящий отказ сборки или записи конфига.
- Панель `web/app.js`: диалог OAuth на 400 от `authorize/status` должен
  показывать текст ошибки, а не крутиться; `PUT /api/sets/<id>` — читать
  `failed`. Код панели не правился (вне зоны).
- `docs/history/index.md`: строка про этот отчёт.

## Ворота

- `go build ./...` — без вывода.
- `go vet ./...` — без вывода.
- `~/go/bin/golangci-lint run --timeout=10m` — 0 issues.
- `go test ./internal/manager/... ./internal/toolout/... -race -count=1 -p 1` —
  214 passed (после закрытия #40: manager 205 + toolout 9).

Прогон шёл параллельно с соседней полосой (`internal/mcpserver`): дважды
`go vet ./...` и `golangci-lint` падали на её файлах в момент правки
(`m.timeoutText`/`m.callTimeout` undefined, неиспользуемый импорт в
`refresh_test.go`). Все четыре ворот прогнаны ещё раз после того, как дерево
собралось; результаты выше — с чистого дерева.
