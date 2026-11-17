# `panel --help`: полный список переменных окружения (2026-11-17)

Зона — `cmd/mcprig/`, `docs/{en,ru}/cli.md`, `CHANGELOG.md`, `docs/history/`.
Замечание ревью: `mcprig panel --help` печатал 20 строк, а панель читала больше —
в том числе `XDG_STATE_HOME` и шесть переменных OpenBao, — и число в
`docs/{en,ru}/cli.md` не совпадало с таблицей помощи. `internal/` и `web/` не
правились.

## 1. Что панель читает на самом деле

Сверено по коду, а не по докам:

- `internal/manager/api.go:66-95` (`FromEnv`) — 17 `MCPRIG_*`; каталог состояния
  по умолчанию — `defaultDataDir` (`internal/manager/api.go:97-106`), она читает
  `XDG_STATE_HOME`, и `MCPRIG_DATA_DIR` её перекрывает;
- `internal/manager/secrets.go:115-128` (`newBaoFromEnv`) — `BAO_ADDR`/
  `OPENBAO_ADDR`, `BAO_TOKEN`/`OPENBAO_TOKEN`, `BAO_MOUNT` (по умолчанию
  `secret`), `BAO_PREFIX` (по умолчанию `mcp`); клиент поднимается из
  `manager.NewSecrets` (`internal/manager/secrets.go:253-258`), которую панель
  зовёт в `manager.New` (`internal/manager/api.go:178`);
- `internal/toolout/toolout.go:62,72` — `MCPRIG_TOOLOUT_TTL` (`24h`) и
  `MCPRIG_TOOLOUT_MAX` (`256MiB`); `cmd/mcprig/main.go:38` — `MCPRIG_PPROF`.

Чего в коде нет и что поэтому в таблицу не попало:

- `BAO_CACERT` и `BAO_NAMESPACE` — `newBaoClient`
  (`internal/manager/secrets.go:132-142`) берёт `http.DefaultTransport` без
  настроек TLS и namespace: такие значения сейчас молча не действуют;
- `MANAGER_SEED_FILE` (`internal/registry/store.go:82`) — служебная переменная,
  её выставляет сама панель из `MCPRIG_SEED_FILE`
  (`internal/manager/api.go:172`), оператору она не нужна;
- `OTEL_EXPORTER_OTLP_ENDPOINT`/`OTEL_SERVICE_NAME` — это `internal/mcpserver`,
  то есть процесс набора, а не панель;
- `MCPRIG_PANEL_URL`, `MCPRIG_TOKEN`, `MCPRIG_KEY` — клиентские (`status`,
  `logs`, `warm`, `approve`, `init`), панель их не читает.

## 2. Правка

`panelEnv` (`cmd/mcprig/main.go:120-153`) переписан: 27 строк, порядок
сгруппирован — пути (`MCPRIG_DATA_DIR`, `XDG_STATE_HOME`, `MCPRIG_CLI`,
`MCPRIG_SEED_FILE`), сеть (`MCPRIG_HOST`, `MCPRIG_PORT`, `MCPRIG_PUBLIC_URL`,
`MCPRIG_BIND_IP`), вход (`MCPRIG_PASSWORD`, `MCPRIG_PASSWORD_HASH`,
`MCPRIG_USER`, `MCPRIG_AUTH`), TLS (`MCPRIG_TLS_CERT`, `MCPRIG_TLS_KEY`), прокси
(`MCPRIG_PROXY_SECRET`, `MCPRIG_TRUSTED_PROXIES`, `MCPRIG_USER_HEADER`,
`MCPRIG_ALLOWED_HOSTS`), OpenBao (`BAO_ADDR`, `OPENBAO_ADDR`, `BAO_TOKEN`,
`OPENBAO_TOKEN`, `BAO_MOUNT`, `BAO_PREFIX`), прочее (`MCPRIG_TOOLOUT_TTL`,
`MCPRIG_TOOLOUT_MAX`, `MCPRIG_PPROF`). Каждая строка — «что задаёт, что по
умолчанию»; причины группировки и договор о числе записаны в комментарии к
таблице.

Описания `MCPRIG_SEED_FILE` и `MCPRIG_TRUSTED_PROXIES` проверены по коду и
остались верными; у `MCPRIG_SEED_FILE` добавлено «пусто — реестр остаётся
пустым», у `MCPRIG_PASSWORD`/`MCPRIG_AUTH` — что при пустом пароле нужен
прокси-секрет или `MCPRIG_AUTH=off` (иначе панель не стартует,
`internal/manager`).

`docs/{en,ru}/cli.md`, раздел `panel`: «twenty»/«двадцать» → двадцать семь,
таблица переставлена в тот же порядок, что печатает `--help`, добавлены
`XDG_STATE_HOME`, `BAO_ADDR`/`OPENBAO_ADDR`, `BAO_TOKEN`/`OPENBAO_TOKEN`,
`BAO_MOUNT`, `BAO_PREFIX` — 24 строки таблицы плюс три переменные, описанные
рядом (`MCPRIG_TOOLOUT_TTL`, `MCPRIG_TOOLOUT_MAX`, `MCPRIG_PPROF`), итого 27.
`CHANGELOG.md` — одна строка под `[Unreleased]` (прежняя, про «twenty», заменена,
а не продублирована).

Тест `TestPanelHelpListsEnv` (`cmd/mcprig/main_test.go:49-76`) дополнен: ищет
новые имена в выводе, сверяет `len(panelEnv) == 27` (то же число записано в
доках) и требует непустые, уникальные имена с описаниями.

`cmd/mcprig/codemap.md` — ссылки `main.go:N` после таблицы сдвинулись на 10
строк и приведены к коду заново; строка про `panel` называет и `panelEnv`.

## 3. Ворота

```
$ go build ./... && go vet ./... && go test ./cmd/... -count=1
Go test: 65 passed in 2 packages          # exit 0
```

Живой прогон нового бинарника (боевой `~/.local/bin/mcprig` не подменялся,
сервис не перезапускался):

```
$ go build -o /tmp/opencode/mcprig-new ./cmd/mcprig
$ /tmp/opencode/mcprig-new panel --help; echo "exit=$?"
usage: mcprig panel
панель настраивается переменными окружения:
  MCPRIG_DATA_DIR          реестр, ключи, логи, токены OAuth (по умолчанию $XDG_STATE_HOME/mcprig)
  XDG_STATE_HOME           база каталога состояния, если MCPRIG_DATA_DIR пуст (по умолчанию ~/.local/state)
  …
  MCPRIG_TOOLOUT_TTL       срок жизни обрезанных ответов в tool-out (24h)
  MCPRIG_TOOLOUT_MAX       объём tool-out, K/M/G (256MiB)
  MCPRIG_PPROF             адрес pprof-сервера, например 127.0.0.1:6060 (пусто — выключен)
exit=0
```

(`${XDG_STATE_HOME}` в первой строке печатается как текст: подстановки нет,
это часть описания.) Аргумент-нарушитель:

```
$ /tmp/opencode/mcprig-new panel foo; echo "exit=$?"
mcprig: лишние аргументы: foo; команда принимает: serve|stdio|auth|approve|panel|init|doctor|warm|status|logs
exit=2
```

```
$ bash scripts/docs-check.sh; echo "exit=$?"
ok   links resolve
ok   no personal paths
ok   no orphans
ok   language parity
ok   anchors (10)
exit=0
```

## 4. Что осталось

`docs/history/index.md:67` и `docs/history/audits/2026-11-16-codemap-drift.md:72`
по-прежнему говорят «двадцать переменных»: это записи о прошлых полосах, и они
не переписывались — история фиксирует состояние на свою дату.

Если `BAO_CACERT`/`BAO_NAMESPACE` понадобятся, это правка
`internal/manager/secrets.go:132-142` (TLS-конфиг и заголовок `X-Vault-Namespace`)
плюс две строки в `panelEnv` и в обеих доках.
