# 0002. Возможности прокси: очередь улучшений после 0.4.0

Status: proposed
Date: 2026-11-15

## Что это

План на все найденные улучшения, сфокусированный на возможностях прокси, в двух
слоях: быстрые победы (до одного дня каждая) и крупные ставки (недели). Собран по
аудитам сопровождаемости и DX, стратегической оценке, внешнему сравнению
MCP-шлюзов и проверке безопасности восьми изменений. Утверждения сверены с кодом;
три из них после сверки пришлось поправить — правки внесены здесь.

Порядок и содержание — предложение, а не принятое решение. Приёмка этапа —
человеком; провенанс каждой строки: `agent-proposed`, если не указано иное.

## Что уже есть и не требует работы

- Кэш каталога инструментов и реакция на `notifications/tools/list_changed`:
  `internal/mcpserver/manager.go:133-138,228-233,1024-1033`,
  `internal/downstream/connect.go:99-110`; тест `internal/mcpserver/livecache_test.go:73`.
- Гейт прав по правилам `permissions` для вызовов через прокси:
  `internal/mcpserver/manager.go:751`, `internal/mcpserver/invokemany.go:155`.
- Таймауты вызова, отмена вверх (`internal/mcpserver/manager.go:797,1272,1324`),
  усечение с выгрузкой (`internal/mcpserver/toolout.go`), `structuredContent`.
- Песочница bwrap, OAuth из панели и из терминала, статистика с историей.

## Слой 0. Гигиена (1 день)

Без этого следующие этапы опираются на неверные тексты и обходят политику.

1. **Гейт `permissions` в панельном browse.** `internal/manager/browse.go:241-252`
   зовёт `sess.CallTool` без `denyReason`; гейт применяется только в
   `internal/mcpserver/manager.go:751` и `internal/mcpserver/invokemany.go:155`.
   Запрещённый в конфиге инструмент вызывается из панели. Вынести проверку в
   общий пакет (например `internal/config` рядом с `Permissions`) и звать её в
   `browser.callTool`/`readResource`/`getPrompt`.
   Сделано: `config.DenyReason` зовётся в `browse.callTool`, правила
   сохраняются реестром. `readResource`/`getPrompt` отложены до D сознательно:
   словарь `permissions` — про имена инструментов. UI-редактор `permissions` —
   follow-up.
2. **Устаревшие тексты.** Четыре страницы цитируют старый ответ CSRF («нужен
   заголовок Origin»), код отвечает «не хватает заголовка Origin»
   (`internal/manager/api.go:660`): `docs/en/panel.md:278`,
   `docs/en/troubleshooting.md:131`, `docs/ru/panel.md:278`,
   `docs/ru/troubleshooting.md:130`.
   Сделано: точный текст цитируется в четырёх страницах.
3. **`docs/CURRENT-GOAL.md:31-34`** («Остаток» v0.3.4): оба пункта закрыты —
   Origin проверяется при пустом заголовке (`api.go:650-660`), journald получает
   строки о падении и перезапуске.
   Сделано: «Остаток» переписан как закрытый, с настоящими номерами строк.
4. **Мёртвое:** `Secrets.OpenBao()` без вызовов (`internal/manager/secrets.go:247`);
   `Logging` пишется в `servers.json`, поля в конфиге нет
   (`internal/manager/setconfig.go:43,74`).
   Сделано: удалены, `configuration.md` больше не обещает блок `logging`.
5. **`_configError` с json-тегом** (`internal/config/config.go:43`): попав в
   `registry.json`, сервер навсегда считается битым
   (`internal/mcpserver/manager.go:1054`). Снять тег или чистить поле при записи.
   Сделано: тег `json:"-"`, тест на заражённый файл, ключ убран из мока панели.

Ворота: `go build ./... && go vet ./... && go test ./... -race -count=1 -p 1`,
`bash scripts/docs-check.sh`. Пройдены 2026-11-15: 434 теста в 15 пакетах,
docs-check 5/5; коммит `0e65538`.

## Слой 1. Быстрые победы прокси (3–4 дня)

### T1. Полный ответ по HTTP, а не путём на диске (0,5 дня)

Сейчас усечённый ответ выгружается в `<data>/sets/<id>/tool-out/`
(`internal/toolout/toolout.go:39-47`, `internal/manager/runtime.go:587-590`), и для
харнесса на другой машине этот путь бесполезен.

- Маршрут `GET /tool-out/{name}` на HTTP-листенере набора
  (`internal/httplisten/httplisten.go`), `name` — `^[0-9a-f]{16}(\.txt)?$`.
- Bearer обязателен **и в открытом наборе**: маршрут не наследует
  `AllowAnonymous` (`internal/manager/setconfig.go:71`).
- Заголовки `text/plain; charset=utf-8`, `X-Content-Type-Options: nosniff`,
  `Cache-Control: no-store`, `Referrer-Policy: no-referrer`; потоковая отдача с
  потолком, `410` после TTL 24 ч (`internal/toolout/toolout.go:20-29`).
- В маркере усечения (`internal/mcpserver/toolout.go:61`) рядом с путём печатать
  URL.
- `shortcut: имя файла не привязано к ключу; привязать к отпечатку выдающего
  ключа, когда появится мультиключ (B).`

Тесты: `TestToolOutServedWithBearer`, `TestToolOutRefusedOnOpenSetWithoutBearer`,
`TestToolOutRejectsTraversal`.

Сделано (2026-11-15): маршрут в `internal/httplisten/httplisten.go` (имя без
`.txt`, `allowAnonymous=false` жёстко), заголовки и Range — через
`http.ServeContent`, `404`/`410` по mtime, маркер называет URL при HTTP+ключе.
Открытый набор файл не отдаёт вовсе: маркер там остаётся путём — это не дыра, а
осознанная граница.

### T2. Лимиты по ключу и инструменту (1 день)

Лимитов нет ни на вызовы, ни на панель (только логин,
`internal/manager/auth.go:184-192`).

- Поле набора `rateLimit`: `{"perMinute": 120, "burst": 20, "perTool": {"server/tool": {"perMinute": 10}}}`;
  пустое — без лимита (поведение сегодняшнего дня).
- Корзина `golang.org/x/time/rate`, ключ — отпечаток ключа из `stats.ClientFrom`
  (`internal/stats/client.go:35-38`) плюс имя инструмента; `_meta` clientInfo
  идентичностью не считается (`internal/mcpserver/manager.go:852-868`).
- Считать каждый внутренний вызов `invoke_many` (до 8 за запрос,
  `internal/mcpserver/invokemany.go:19-21`).
- Отказ — ошибка инструмента с повтором через N мс и счётчик `throttled` в
  `/stats`; на входе мета-тула, до открытия сессии.

Тесты: `TestRateLimitPerKeyIndependent`, `TestInvokeManyCountsEachInnerCall`,
`TestRateLimitOffByDefault`.

Сделано (2026-11-15): корзина на отпечаток ключа (в открытом наборе — на адрес
клиента, stdio — на процесс), ключ корзины — `sha256` 16 hex, потолок 4096 с
вытеснением внутри своей идентичности; отказ не тратит токен; `throttled` в
`/stats` и в `/api/calls`. Лимит задуман как тормоз против случайного веера:
`shortcut:` в коде и раздел в `configuration.md` прямо это говорят.

### T3. Профили инструментов на ключ (1 день)

- Поле набора `profiles: {"reader": {"deny": ["*/*_write"], "allow": ["*"]}}`,
  у ключа — `profile`.
- `list_commands` фильтрует по профилю, `invoke_command`/`invoke_many` отвергают с
  текстом профиля. Правила профиля разрешаются **после** правил `permissions`:
  профиль сужает, не расширяет.
- Панель: выбор профиля при выдаче ключа (`POST /api/sets/<id>/keys`).

Тесты: `TestProfileFiltersListCommands`, `TestProfileCannotWidenPermissions`,
`TestProfileDeniesInvoke`.

Сделано (2026-11-15): `profiles` в записи набора (`registry.Set`), профиль у
ключа (`registry.Key.Profile`), выдача `POST /api/sets/<id>/keys` с полем
`profile` (неизвестное имя — `400` с перечнем), гейт в `internal/mcpserver` после
`permissions`, фильтр `list_commands`; имя профиля, которого у набора больше нет,
закрывает ключу всё. Панель: select профиля в форме выдачи и бейдж у ключа,
демо-мок отвечает как демон. `describe_commands` профилем не фильтруется
(имена скрыты, схема по угаданному имени отдаётся) — известное ограничение;
browse-сессии панели профиль не применяют, у них нет ключа набора.

Это удобство для одного оператора, не изоляция клиентов: все ключи набора
эквивалентны по правам, пока не появится модель авторизации на ключ.

### T4. Уведомление о падении набора (0,5–1 день)

- Поле набора `notify: {"webhook": "https://…"}`.
- События: `set.crashed` (код выхода), `set.restart_failed` (исчерпаны 5 попыток,
  `internal/manager/runtime.go:278`), `server.unhealthy`
  (`internal/manager/health.go:293`).
- POST JSON `{"event","set","server","code","at"}`, таймаут 5 с, три попытки с
  backoff, не чаще одного события на переход; секреты в тело не попадают.

Тесты: `TestNotifyOnCrashOncePerTransition`, `TestNotifyNoSecretLeak`.

Сделано (2026-11-15): поле `notify` живёт в записи набора (`registry.Set`),
разбор в `buildSet`, события шлёт `internal/manager/notify.go` (5 с, три
попытки, дедупликация перехода в памяти); выключено, пока блок не задан.
Панель поле пока не показывает — правится через `registry.json` или API.

### T5. `mcprig status|logs --json` (1 день)

Сейчас `--json` есть только у `doctor` и `warm`; управление наборами — только
через HTTP API.

- `mcprig status --panel <url> [--json]` — наборы, состояние, версия, счётчики.
- `mcprig logs --set <id> [-f] [--json]` — хвост `sets/<id>/mcprig.log`.
- Вход — тем же способом, что у `warm` (`cmd/mcprig/warm.go:61-77`).

Тесты: `TestStatusJSONShape`, `TestLogsFollow`.

Сделано (2026-11-15): `status` (состояние, порт, инструменты, ключ, последний
вызов) и `logs` (`--set`, `--server`, `-n`, `-f`, `--json`), вход вынесен в
`cmd/mcprig/panel.go` и общий с `warm`. Сокращено осознанно: **версии нет** —
`/api/state` её не отдаёт; счётчики вызовов не печатаются, хватает последнего
вызова, полные числа доступны через `/api/stats`. Версию добавить вместе с E
(OTel), где `/api/state` всё равно расширяется.

### T6. UI для существующих маршрутов и пагинация вызовов (1 день)

- Дровер лога отдельного сервера: маршрут есть
  (`internal/manager/api.go:556,1403`), в `web/` потребителя нет.
- `/api/calls` — добавить `before`/`after` (время последней записи), панель
  грузит по 100 и подгружает дальше; фильтр остаётся клиентским.

Тесты: `TestCallsPagination`; UI проверен руками по моку и снимками, автотеста панели нет.

Сделано (2026-11-15): `/api/calls` принимает `before`/`after` (RFC3339, оба
сразу → 400), панель грузит по 100 и подгружает «Показать ещё»; дровер лога
сервера читает `/api/sets/<id>/servers/<name>/logs`. Глубина страниц ограничена
хвостом `/stats` (512 вызовов) — глубже `/api/stats`.

## Слой 2. Крупные ставки

### A. Правка одного сервера без перезапуска набора (2 недели)

Самая дорогая и самая ценная: правка сервера, env, OAuth и даже подписи ключа
перезапускает набор целиком (`internal/manager/api.go:1449,1500-1506,1602,1619`,
`internal/manager/oauth.go:126`).

1. Обработчик SIGHUP. Сейчас зарегистрированы только INT/TERM
   (`cmd/mcprig/main.go:270,340`), и сигнал убивает процесс.
2. Перечитать конфиг целиком и применить **fail-closed**: ошибка валидации —
   остаться на старом, поднять счётчик `configGeneration` в `/healthz`.
3. Диф серверов: добавлен — лениво; удалён — закрыть сессию и сбросить кэш
   инструментов; изменены `permissions`/`enabled`/таймаут — обновить на месте;
   изменены `command`/`cwd`/`url`/`env`/`sandbox` — закрыть сессию этого сервера и
   поднять заново с дренажом в полёте (образец — `internal/manager/browse.go:178-207`).
4. Панель вместо полного перезапуска шлёт reload и показывает его результат.

Ворота: e2e `TestReloadKeepsOtherSessions` — два сервера, живая сессия к первому,
правка второго через API; сессия первого жива, `restarts` набора не вырос.

Риск: конфиг сейчас иммутабелен на время жизни процесса; «частично применено» надо
показывать честно, а не молча.

Сделано (2026-11-15): SIGHUP обрабатывается (`cmd/mcprig/main.go`), процесс
перечитывает конфиг и публикует ack `reload.status` с `nonce`, `generation`,
`applied`, `restartNeeded`; сигнал идёт только лидеру процесса набора, stdio-дети
переживают правку соседа. Диф по имени: добавлен лениво, удалён — сессия
закрывается, смена launch-полей — новая запись, `permissions`/`rateLimit`/
`profiles` — на месте. Супервизор шлёт `POST /api/sets/<id>/reload` (`Runtime.Reload`,
бюджет 3 с), а `PUT /api/servers/<name>` отвечает `reloaded`/`reloadErrors`;
`sandbox-e2e.sh` и `go test -race` зелёные. Правка `env`/`envAllow` перезапускает
наборы с этим сервером (окружение процесса набора фиксируется при старте), так же
поступает смена `authToken`; `path`/`host`/`allowedHosts` из ручной правки
`servers.json` попадают в `restartNeeded`, перезапуск — вручную.

### B. Несколько активных ключей на набор (1 неделя, после A)

Сейчас ровно один активный ключ: `normalizeKeys` отзывает остальные
(`internal/registry/keys.go:10-17,70-80`), `IssueKey` отзывает текущий
(`internal/registry/store.go:490-516`), отзыв активного **останавливает** набор
(`internal/manager/api.go:1543-1547`), выдача и переименование — перезапускают
(`api.go:1447-1453,1500-1506`).

- Транспорт: список токенов вместо одного (`internal/httplisten/httplisten.go:196-203`),
  отдельный «основной» ключ для внутренних опросов `/stats` и health
  (`internal/manager/calls.go:167-169`, `internal/manager/api.go:212`).
- Доставка нового списка — тем же reload, что в A. Без этого отозванный ключ
  работает до перезапуска, то есть отзыв перестаёт быть реакцией на утечку.
- Отзыв последнего активного ключа — по-прежнему остановка набора.

Ворота: `TestRevokeOneKeyKeepsOthers`, `TestTokenListAppliedWithoutRestart`,
`TestLastKeyRevokeStopsSet`.

### C. TLS и явный `publicUrl` (3 дня)

- Админский параметр `publicUrl` — единственный источник redirect и ссылок.
  Сейчас `callbackURL` собирается из `Host` и первого non-loopback IPv4
  (`internal/manager/oauth.go:217-243`), поэтому провайдер с
  `invalid_redirect_uri` требует TLS.
- `X-Forwarded-Host` доверять только при заданном `MCPRIG_PROXY_SECRET`
  (`internal/manager/api.go:711-726,735-743`).
- Необязательный `tls {cert,key}` на листенер панели; при https публичном адресе
  cookie сессии — `Secure` (`internal/manager/auth.go:407-433`).
- `doctor` печатает эффективный redirect и ругается, если он указывает в пустоту.

Ворота: `TestCallbackURLFromPublicURL`, `TestXFHIgnoredWithoutProxySecret`,
`TestSecureCookieBehindProxy`.

Сделано (2026-11-15): `publicUrl` валидируется и попадает в `CallbackURL`
(префикс пути сохраняется); `tls {cert,key}` поднимает `ListenAndServeTLS`
(`MinVersion` 1.2), cookie сессии становится `Secure` при https-адресе или TLS;
`X-Forwarded-*` принимаются только при заданном `MCPRIG_PROXY_SECRET`, и это
задокументированная цена доверия; `doctor` печатает effective redirect.
Лимит входа считается по адресу сокета и по `X-Forwarded-For` одновременно.

### D. Ресурсы и промпты для MCP-клиента (1 неделя)

Панель их уже перечисляет и читает (`internal/manager/browse.go:154-269`), а
MCP-клиент видит только tools: пять `AddTool` в
`internal/mcpserver/manager.go:258-337`.

- Поле сервера `expose: ["tools","resources","prompts"]`, по умолчанию `["tools"]`.
- Мета-тулы `list_resources` и `read_resource`; привязка URI к серверу — явным
  параметром, без глобальной карты URI→сервер.
- Тот же потолок и выгрузка, что у tool-ответов (`internal/mcpserver/toolout.go:35`
  сейчас работает только с `CallToolResult`).
- `permissions` получает вид правила (`tool|resource|prompt`); сервер с
  `default: deny` не отдаёт ресурсы, пока не разрешено явно.

Ворота: `TestResourceReadRequiresExpose`, `TestResourceTruncatedAndSpilled`,
`TestPermissionsApplyToResources`.

### E. OTel: `traceparent` вниз (1 неделя)

- Принимать `traceparent` из `_meta` клиента, проверять формат W3C, пробрасывать
  только его в вызовы инструментов (сейчас `_meta` вниз не передаётся вообще).
- Экспорт — по `OTEL_EXPORTER_OTLP_ENDPOINT`; без переменной — только проброс.
- Трасса от клиента помечается источником: доказательством для аудита не считается.

Ворота: `TestTraceparentValidated`, `TestMetaNotForwardedWholesale`.

### F. Quarantine и смена дескриптора (1 неделя)

- Хэш `name+description+inputSchema+outputSchema+annotations` на пару
  (сервер, инструмент), файл `sets/<id>/tools.lock.json`, запись атомарная.
- Изменение хэша — блокировка вызова **в `invoke_command` и `invoke_many`**, а не
  только в `list_commands`: `invoke_command` проверяет членство лишь при непустом
  кэше (`internal/mcpserver/manager.go:757-759`).
- Одобрение — кнопкой в панели и `mcprig approve --set <id> <server/tool>`,
  привязано к точному хэшу.
- Честная граница: смена поведения при том же дескрипторе не детектируется.

Ворота: `TestChangedDescriptorBlockedUntilApprove`, `TestApproveBindsToHash`.

## Порядок и сумма

| этап | содержание | цена | зависит от |
| --- | --- | --- | --- |
| 0 | гигиена | 1 день | — |
| 1 | T1–T6 быстрые победы | 3–4 дня | 0 |
| A | reload без перезапуска | 2 недели | 0 |
| B | мультиключ | 1 неделя | A (доставка списка токенов) |
| C | TLS и `publicUrl` | 3 дня | — |
| D | ресурсы и промпты | 1 неделя | A, расширение `permissions` |
| E | OTel | 1 неделя | — |
| F | quarantine | 1 неделя | A |

Итого около 5–6 недель одного разработчика. Если брать только две ставки — A и
B: вместе они дают «радиус правки = 0» и безопасную ротацию ключей.

## Сознательно не делать

- **Плоский проксинг тулов** (`server__tool` в `tools/list`): убивает главную
  ставку продукта — предсказуемую цену контекста; вернёт N×M схем и размножит
  гейт прав, статистику и усечение.
- **RBAC/SSO в панели**: панель root-эквивалентна (правит `command`, читает
  секреты), роли потребуют проверки на каждом маршруте, а читающий export уже
  видит секреты.
- **Контейнерный рантайм песочницы**: bwrap — один бинарь без демона; сокет
  рантайма сам root-эквивалентен.
- **Кэш результатов инструментов**: ломает семантику побочных эффектов.
- **Свой реестр/маркетплейс**: роль каталога играет `registry`.

## Критерий «стало лучше»

Радиус правки = 0: за месяц работы правка одного сервера или подписи ключа не
рвёт ни одной чужой сессии и ни одного вызова в полёте. Мерить: счётчик
`restarts` в `/stats` набора против числа правок; базовую линию снять за неделю
до этапа A.

Страж, чтобы не купить это ценой ядра: p95 накладных расходов прокси < 25 мс и
`schemaTokens` в `/api/stats` не растут.

## Открытые вопросы (решает владелец)

1. A или C первой? Предложение: A, TLS закрывается прокси на неделю раньше.
2. Профили (T3) — удобство или заявка на изоляцию клиентов? Если второе, нужна
   авторизация на ключ, и это отдельная модель, а не фильтр.
3. Попутные дефекты слоя 0 — отдельными issue или одной пачкой «гигиена»?
