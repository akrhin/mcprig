# Полоса «мелкие долги»: D10, D6, D15 (2026-11-17)

Зона — `cmd/mcprig/`, `web/` и тесты. Основание — три незакрытых пункта
[codemap-дрейфа 2026-11-16](2026-11-16-codemap-drift.md). `docs/en/`, `docs/ru/`
кроме этого файла и строки в [../index.md](../index.md) не правились: расхождения
документации перечислены в разделе 4 для отдельной полосы.

## 1. D10 — `mcprig panel --help`

`cmd/mcprig/main.go` — новый `newPanelFlags()`: пустой `flag.FlagSet` с
собственным `Usage`, который печатает таблицу переменных окружения. `parseArgs`
для `panel` теперь зовёт `parseFlags(newPanelFlags(), args[1:])`, поэтому `-h` и
`--help` отдают `flag.ErrHelp` (код 0), а любой другой аргумент по-прежнему
`usageError` (код 2). Список — `panelEnv` в том же файле: 17 переменных
`manager.FromEnv` (`internal/manager/api.go:66-95`) плюс `MCPRIG_TOOLOUT_TTL`,
`MCPRIG_TOOLOUT_MAX` (`internal/toolout/toolout.go:62,72`) и `MCPRIG_PPROF`
(`cmd/mcprig/main.go:38`), которые читает тот же режим.

Вывод `./mcprig panel --help` (код 0):

```text
usage: mcprig panel
панель настраивается переменными окружения:
  MCPRIG_DATA_DIR          реестр, ключи, логи, токены OAuth (по умолчанию $XDG_STATE_HOME/mcprig)
  MCPRIG_HOST              адрес прослушивания панели (127.0.0.1)
  MCPRIG_PORT              порт панели (9091)
  MCPRIG_PUBLIC_URL        публичный адрес панели: источник OAuth redirect и Secure-cookie
  MCPRIG_TLS_CERT          сертификат панели, только вместе с MCPRIG_TLS_KEY
  MCPRIG_TLS_KEY           ключ панели, только вместе с MCPRIG_TLS_CERT
  MCPRIG_CLI               бинарник набора (по умолчанию текущий исполняемый файл)
  MCPRIG_BIND_IP           transport.host в servers.json каждого набора (0.0.0.0)
  MCPRIG_SEED_FILE         JSON servers[]/sets[] для пустого реестра
  MCPRIG_PASSWORD          пароль входа в панель
  MCPRIG_PASSWORD_HASH     scrypt-хеш пароля; задан — пароль из MCPRIG_PASSWORD не читается
  MCPRIG_PROXY_SECRET      общий секрет с доверенным прокси, короче 32 символов — предупреждение
  MCPRIG_TRUSTED_PROXIES   IP/CIDR через запятую, чьи X-Forwarded-* верят (loopback)
  MCPRIG_USER              имя входа (admin)
  MCPRIG_USER_HEADER       заголовок с именем пользователя от прокси (Remote-User)
  MCPRIG_ALLOWED_HOSTS     Host через запятую (только loopback-хосты)
  MCPRIG_AUTH              off — без входа в панель
  MCPRIG_TOOLOUT_TTL       срок жизни обрезанных ответов в tool-out
  MCPRIG_TOOLOUT_MAX       объём tool-out, K/M/G
  MCPRIG_PPROF             адрес pprof-сервера, например 127.0.0.1:6060
```

`mcprig panel foo` → `mcprig: лишние аргументы: foo; команда принимает: …`, код 2
(текст даёт общий `parseFlags`).

Тесты: `TestPanelHelpListsEnv` (буфер `FlagSet`, `-h` → `flag.ErrHelp`, в выводе
есть `MCPRIG_DATA_DIR`/`MCPRIG_PORT`/`MCPRIG_PASSWORD`/`MCPRIG_AUTH`/`MCPRIG_TOOLOUT_TTL`)
и `panel -h`/`panel --help` в `TestHelpIsNotAnError` (код 0).

## 2. D6 — поле подписи ключа

Панель переведена на `label`, бэкенд по-прежнему принимает `note`:

- `web/app.js` читает `k.label` (строка ключа, скрытие кнопки **Save**, сравнение
  при вводе) и шлёт `{"label":…}` в `PATCH /api/sets/<id>/keys/<keyID>`.
- Выдача ключа одним запросом: `POST /api/sets/<id>/keys` получает
  `{"profile":…,"label":…}` — бэкенд читал `label` в `keyIssueInput`
  (`internal/manager/api.go:1836-1856`) и раньше, поэтому второй `PATCH` убран:
  набор перезагружается один раз. Прежний комментарий «выдача подписью не
  принимает» был неверен и удалён.
- `web/mock/state.json` — ключи демо-режима отдают `label` (как живой `/api/state`).
- Совместимость на бэкенде осталась и помечена: `internal/registry/types.go:105-108`
  (`Note` в `PublicKey` — только для старых клиентов), `internal/manager/api.go:1843-1847`
  (приём `note` при выдаче), `internal/manager/api.go:1881-1883` (PATCH),
  `internal/registry/store.go:744` по-прежнему заполняет `note` из `label`.

Проверка на живом бинаре (панель на `127.0.0.1:9111`, `MCPRIG_AUTH=off`, отдельный
`MCPRIG_DATA_DIR`, браузер через playwright):

- ключ, выданный формой с подписью `проверка-label`, показан в строке ключа с этим
  значением (`#key-list .key-note .input` = `проверка-label`);
- правка подписи в строке (`ci-метка` + **Save**) сохранена и вернулась после
  перезагрузки состояния; кнопка **Save** снова скрыта;
- `GET /api/state` после обеих операций: `[{"id":"5ee2f66fcb82","label":"ci-метка","note":"ci-метка"},
  {"id":"6ba8f4aaf2be","label":"проверка-label","note":"проверка-label"}]` — новый код
  читает `label`, `note` остаётся зеркалом для старых клиентов.

## 3. D15 — устаревшие комментарии

- `web/app.js:1-9` — шапка описывала `/api/calls` без set раз в 15 с. Сейчас:
  `/api/state` 5 с; выбранный набор — health и `/api/calls` 5 с, кроме листаемой
  истории; «Статистика» — `/api/stats` 15 с, пока вкладка открыта; лог — по кнопке
  и 10 с, пока набор запущен; `/api/env` — 30 с; скрытая вкладка не опрашивается
  (`every()` в `web/app.js:4254`). Всё это и написано.
- `web/embed.go:13-15` — `fs.Sub(assets, ".")` каталог `mock/` не срезает: он
  остаётся, и `servePanel` его пускает (`internal/manager/panel.go:36`). Комментарий
  переписан: статика отдаётся целиком, вместе с `mock/`, откуда `?mock=1` берёт
  ответы `/api`.

## 4. Что должна дописать полоса документации

- `docs/en/cli.md:139` и `docs/ru/cli.md:136` — «No flags. Everything is
  environment:» / «Флагов нет. Всё задаётся окружением:»: добавить, что `panel`
  понимает `-h/--help` (печатает этот же список переменных, код 0), а прочие
  аргументы — код 2.
- `docs/en/panel.md:114-121` и `docs/ru/panel.md:118-123` — выдача ключа шлёт
  `{"profile":…,"label":…}` одним `POST`, второй `PATCH` не делается; правка
  подписи шлёт `{"label":…}`, а не `{"note":…}` (и слово «note» в этих абзацах
  теперь значит `label`).
- `docs/en/panel.md:305` и `docs/ru/panel.md:311` уже верны (`{"label":…}`,
  `{"note":…}` принимается) — править не нужно.

## 5. Ворота

```text
go build ./...                             ok
go vet ./...                               ok
~/go/bin/golangci-lint run --timeout=10m   0 issues
go test ./... -race -count=1 -p 1          Go test: 702 passed in 17 packages
node --check web/app.js                    ok
```
