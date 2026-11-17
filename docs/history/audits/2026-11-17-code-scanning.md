# Code scanning: разбор алертов GitHub, 17.11.2026

Задача владельца: «настроил workflow в гитхабе, а толку если ты не проверил: …/security/code-scanning — надо все проверять, писать в долги, потом исправлять».

Снимок на 2026-11-17 по `repos/akrhin/mcprig/code-scanning/alerts?state=open`: **112 открытых алертов** после релиза 0.6.4. CodeQL (`security-and-quality`) на Go и JavaScript/TypeScript, `.github/workflows/codeql.yml`, cron еженедельно.

Соседние поверхности проверены в тот же час: Dependabot — 0 открытых; secret scanning — **выключен в репозитории** (правило `.gitleaks.toml` в CI закрывает историю, но GitHub-сканер не включён).

## Сводка

| правило | алертов | где | вердикт |
| --- | ---: | --- | --- |
| `js/remote-property-injection` | 66 | `docs/diagrams/*.html` | сгенерированные archify-страницы — исключить из анализа |
| `js/useless-assignment-to-local` | 6 | `docs/diagrams/*.html` | то же |
| `go/log-injection` | 30 | `internal/manager/{api,runtime,setconfig,notify}.go`, `internal/oauth/handler.go`, `internal/httplisten/httplisten.go`, `internal/downstream/connect.go` | чинить: значения из запроса попадают в лог как есть |
| `go/path-injection` | 3 | `internal/manager/stats.go:83`, `internal/stats/history_query.go:436`, `internal/sandbox/sandbox.go:172` | чинить: сегмент пути из URL |
| `go/cookie-secure-not-set` | 2 | `internal/manager/auth.go:490,506` | по замыслу: `Secure` зависит от TLS, `gosec G124` подавлен осознанно |
| `go/request-forgery` | 1 | `internal/manager/notify.go:155` | чинить: схема и вид адреса вебхука не проверяются |
| `go/command-injection` | 1 | `internal/downstream/connect.go:165` | по замыслу: запуск MCP-сервера из конфига — суть продукта |
| `js/client-side-request-forgery` | 1 | `web/app.js:1031` | ложное: адрес собирается из констант панели |
| `js/unused-local-variable` | 1 | `web/app.js:911` | чинить: мёртвая функция `uptime` |
| `js/xss-through-dom` | 1 | `design/ui/index.html:833` | макет-исследование, не продукт — исключить из анализа |

## Долги

| ID | долг | источник | P | объём | риск |
| --- | --- | --- | --- | --- | --- |
| CS1 | Сегмент пути из URL (`id` набора, `period`) идёт в `filepath.Join` без проверки формы | `internal/manager/stats.go:83`, `internal/stats/history_query.go:436` | P2 | 1 хелпер + два вызова | обход каталога, если имя набора придёт не из реестра |
| CS2 | Путь монтирования в песочнице берётся из конфига сервера | `internal/sandbox/sandbox.go:172` | P3 | проверка после `filepath.Clean` | `..` в конфиге — только у того, кто правит реестр |
| CS3 | Вебхук уведомлений: схема и учётные данные в адресе не проверяются | `internal/manager/notify.go:155` | P3 | проверка `http`/`https`, запрет `user:pass@` | SSRF через поле конфига набора |
| CS4 | 30 мест пишут в лог значение из запроса как есть (CR/LF в «одну строку» лога) | `internal/manager/api.go:395,708,746,1539`, `runtime.go:294,323,365,395,553,560`, `setconfig.go:217`, `notify.go:133`, `internal/oauth/handler.go:95,111,151,162,171`, `internal/httplisten/httplisten.go:451,452`, `internal/downstream/connect.go:139,159` | P4 | один санитайзер + 30 точек | подделка строк лога; имена наборов и серверов уже ограничены `namePattern` (`internal/registry/validate.go:15`) |
| CS5 | Мёртвая функция `uptime` в панели | `web/app.js:911` | P4 | удалить | нет |
| CS6 | CodeQL анализирует сгенерированные страницы (`docs/diagrams/*.html` 72 алерта, `design/ui/index.html` 1) | `.github/workflows/codeql.yml` | P4 | `paths-ignore` в конфиге CodeQL | шум закрывает настоящие находки |
| CS7 | Secret scanning на GitHub выключен | репозиторий | P4 | включить в настройках, если владелец хочет | утечка не видна GitHub, но видна gitleaks в CI |

По замыслу и не чинятся: `go/command-injection` (запуск сервера — суть прокси), `go/cookie-secure-not-set` (`Secure` выставляется по факту TLS, на http-панели литерал `true` убил бы вход), `js/client-side-request-forgery` (адрес — константы панели и путь мока).

## Итог после правки (PR #30)

Что закрыто кодом: CS1–CS4, CS6 (сгенерированные страницы ушли из анализа вместе с 72 алертами).

Что осталось и почему закрыто как «по замыслу», а не подгонкой: после правки CodeQL всё ещё показывал 30 `go/log-injection` и один `go/request-forgery`. Причина одна — анализатор не прослеживает поток через обёртку: `safelog.Value` вырезает управляющие символы и покрыт `TestValue`, проверка вебхука стоит до `http.NewRequestWithContext` и покрыта `TestNotifyPostRejectsUnsafeWebhook`, но для CodeQL значение остаётся «пользовательским».

Решение: 31 алерт закрыт как `won't fix` с этим объяснением в комментарии к каждому, а не исключением правила из набора запросов — исключение спрятало бы будущие настоящие находки того же правила, а закрытие по одному оставляет правило в работе. Побочный эффект: после следующего полного сканирования на `master` алерты могут появиться заново с новыми номерами (закрытие привязано к экземпляру алерта), и тогда их надо закрывать снова с той же формулировкой.

## Что делает разбор честным

Каждый алерт прочитан по коду, а не по заголовку правила: `internal/registry/validate.go:15` уже запирает имена наборов и серверов в `^[a-z0-9][a-z0-9_-]{0,63}$`, поэтому часть log-injection и path-injection достижима только через реестр, а не через сеть. Там, где граница уже есть (CS2), долг — формальность для анализатора, и это записано прямо, а не выдано за дефект.
