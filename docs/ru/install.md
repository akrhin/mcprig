[← Docs index](index.md)
[EN](../en/install.md) · RU

Файл охватывает релизные архивы, юниты systemd, Docker и состояние на диске.

# Установка

## Релизные архивы

`.goreleaser.yaml` собирает один статический бинарник на платформу:

| свойство | значение |
| --- | --- |
| ОС / архитектура | linux, darwin × amd64, arm64 |
| архив | `mcprig_<version>_<os>_<arch>.tar.gz` |
| содержимое | `mcprig`, `README.md`, `README.ru.md`, `LICENSE`, `deploy/mcprig.service`, `deploy/mcprig-set@.service`, `deploy/mcprig-warm.service`, `deploy/mcprig-warm.timer` |
| контрольная сумма | `checksums.txt` |
| релиз | `goreleaser` собирает архивы, пушит образы `slim`/`full` в реестр и создаёт черновик релиза Gitea, который публикует человек; релиз на GitHub создаёт воркфлоу; заметки — раздел `CHANGELOG.md` для тега (`scripts/release-notes.sh`) |
| сборка | `CGO_ENABLED=0`, `-trimpath`, `-s -w -X main.version=<version>` |

Релиз выпускается по тегу `v*` в Gitea CI (`.gitea/workflows/release.yml`): он
гоняет проверки — `go vet`, тесты с `-race`, `govulncheck`, `gofmt` — а затем
`goreleaser`, который собирает архивы, пушит образы `slim`/`full` в реестр и
создаёт черновик релиза в Gitea; черновик публикует человек. Воркфлоу GitHub
(`.github/workflows/release.yml`) идёт по тому же тегу: пересобирает архивы с
`goreleaser --skip=publish`, собирает и пушит образы `slim` и `full` в
`ghcr.io/akrhin/mcprig` и заливает архивы через `gh` в
<https://github.com/akrhin/mcprig/releases>; тестов и `govulncheck` там нет,
только `go build ./...`. Тег можно выпустить и руками:
`gh workflow run release.yml -f tag=vX.Y.Z`.

`goreleaser` на хосте разработки не установлен, архивы собирает CI, а не он.
Собрать тот же бинарник руками:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags)" -o mcprig ./cmd/mcprig
```

Обычный `go build -o mcprig ./cmd/mcprig` даёт `dev` в `mcprig -version`.

## systemd (пользовательские юниты)

В `deploy/` лежат три юнита и таймер:

| юнит | что запускает | конфигурация |
| --- | --- | --- |
| `mcprig.service` | `mcprig panel` — реестр, надзор, панель | `~/.config/mcprig/panel.env` |
| `mcprig-set@.service` | `mcprig serve --config …` — один набор отдельной службой | `~/.config/mcprig/sets/<set>/servers.json` и `env` |
| `mcprig-warm.service` + `mcprig-warm.timer` | `mcprig warm --set %i` — держит autostart-набор тёплым каждые 10 минут | `~/.config/mcprig/panel.env` и `env` |

`mcprig-warm.*` поставляются шаблонами: ставьте их как `mcprig-warm@.service` и
`mcprig-warm@.timer` — имя набора несёт `@` — и включайте по таймеру на каждый
autostart-набор.

`mcprig-set@` — альтернатива панели: набор может работать под systemd без
менеджера. Наборы, запущенные из панели, — дочерние процессы панели, а не юниты
systemd.

Юнит панели задаёт `MCPRIG_HOST=127.0.0.1`, `MCPRIG_PORT=9091` и
`MCPRIG_DATA_DIR=%h/.local/state/mcprig`, затем читает `panel.env` для пароля
или прокси-секрета. Юнит набора читает `~/.config/mcprig/sets/%i/env` (держите
его `0600`: там `transport.authToken` и значения `${VAR}`) и запускает
`mcprig serve --config %h/.config/mcprig/sets/%i/servers.json`.

Установка:

```bash
install -Dm755 mcprig ~/.local/bin/mcprig
install -Dm644 deploy/mcprig.service deploy/mcprig-set@.service ~/.config/systemd/user/
install -d -m700 ~/.config/mcprig ~/.local/state/mcprig
printf 'MCPRIG_PASSWORD=%s\n' 'change-me' > ~/.config/mcprig/panel.env
chmod 600 ~/.config/mcprig/panel.env
systemctl --user daemon-reload
systemctl --user enable --now mcprig.service
```

Юниты панели и набора используют `Restart=on-failure`, `NoNewPrivileges=yes` и
`PrivateTmp=yes`; юнит прогрева — `oneshot`, `Restart` у него нет. Панель
останавливается с `TimeoutStopSec=15`, набор — с
`TimeoutStopSec=10`. `systemd-analyze verify` не сообщает ошибок, кроме пути к
ещё не установленному бинарнику.

## Docker

В `Dockerfile` две цели:

| цель | содержимое | нижестоящие |
| --- | --- | --- |
| `slim` (по умолчанию) | бинарник, корневые CA, пользователь без root `10001`, `bwrap` и `prlimit` | stdio-серверы из одного статического бинаря, либо HTTP |
| `full` | `slim` плюс node/npm, python/pip и uv | stdio-серверы, запускаемые через `npx` / `uvx` |

Оба образа опубликованы в GitHub Container Registry — вход не нужен:

```bash
docker pull ghcr.io/akrhin/mcprig:latest        # slim
docker pull ghcr.io/akrhin/mcprig:latest-full   # full
docker pull ghcr.io/akrhin/mcprig:0.4.0-slim    # закреплённый релиз
docker pull ghcr.io/akrhin/mcprig:0.4.0-full
```

`latest` и `latest-full` идут за последним релизом, теги версии закрепляют пару
(`0.4.0-slim`, `0.4.0-full` — текущая).

Либо собрать самому:

```bash
docker build --build-arg VERSION=0.4.0 -t mcprig:slim .
docker build --build-arg VERSION=0.4.0 --target full -t mcprig:full .
docker run --rm -p 127.0.0.1:9100:9100 \
  -v "$PWD/servers.json:/config/servers.json:ro" \
  -e MCPRIG_DATA_DIR=/data \
  mcprig:slim
```

Точка входа образа — `mcprig`; команда по умолчанию —
`serve --config /config/servers.json`; адрес берётся из `transport.host` и `transport.port` — в конфиге должно стоять `0.0.0.0` и тот порт, который публикуешь. `VERSION` — версия **без** ведущего `v`: `0.4.0`, а не `v0.4.0`; значение с `v` отвергается, и сборка падает. Рабочий каталог —
`/data`, владелец — uid 10001; смонтируйте туда том и задайте
`MCPRIG_DATA_DIR=/data`, чтобы токены OAuth и прочее состояние переживали
перезапуск. Оба образа задают `MCPRIG_HOST=0.0.0.0`, поэтому панель внутри
контейнера слушает все интерфейсы: публикуйте её на `MCPRIG_PORT` (по умолчанию
9091, `deploy/docker-compose.yml` держит набор `ops` на 9101). Вне Docker
`MCPRIG_HOST` не задан, и панель остаётся на `127.0.0.1` — не переносите
контейнерное значение в systemd-юнит хоста.

Healthcheck образа спрашивает набор на `http://127.0.0.1:9100/healthz`, а если
тот не отвечает — панель на `http://127.0.0.1:9091/healthz`, так что контейнер
`healthy`, когда отвечает **любой** из них; второй адрес — запасной для
контейнера с одной панелью. Проверка рассчитана на порты по умолчанию, при другом
`transport.port` или `MCPRIG_PORT` задай `--health-cmd` или healthcheck в
compose.

`deploy/docker-compose.yml` запускает два набора (`demo` на 9100, `ops` на 9101)
из `ghcr.io/akrhin/mcprig:latest`, монтирует каждый `servers.json` только для
чтения и держит состояние в именованных томах.

Замерено: `docker build .` без `--target` даёт `slim` — 44 МБ,
бинарь, корневые сертификаты, `bwrap` и `prlimit`; `--target full` добавляет
node/npm, python/pip и uv/uvx и весит 446 МБ. Передавайте релиз в сборку, иначе
бинарь внутри образа отвечает `dev`:
`docker build --build-arg VERSION=$(git describe --tags --abbrev=0 | sed 's/^v//') -t mcprig:slim .`
Оба образа запускались: `slim` отдал удалённую `serena` через сеть
хоста и ответил `find_symbol` через `invoke_command`, healthcheck — `ok`.

`"sandbox": {}` работает в обоих образах: они несут `bubblewrap` для `bwrap` и
`util-linux` для `prlimit` (бинарь запускается по имени, значит должен быть в
`PATH`). Runtime при этом обязан разрешать unprivileged user namespaces; на
хосте с дефолтным seccomp Docker — `--security-opt seccomp=unconfined`. На
rootless-демоне этого мало: `bwrap` падает с `Can't mount proc on /proc:
Operation not permitted`, и замерено рабочим только `--privileged`. `slim` без
node/npm и uv: серверу на `npx` или `uvx` нужен `full`. Сервер с тегом
`untrusted` при умолчании `onUnavailable: deny` по-прежнему не запустится,
если `bwrap` не работает вовсе; серверов без тега это не касается.

## Состояние на диске

`MCPRIG_DATA_DIR` задаёт каталог состояния. По умолчанию:

| процесс | по умолчанию |
| --- | --- |
| `panel` | `$XDG_STATE_HOME/mcprig`, иначе `~/.local/state/mcprig` |
| `serve`, `stdio` | то же правило; `~/.local/state/mcprig` |

Каталог создаётся с правами `0700`. Что там лежит:

| путь | содержимое | режим |
| --- | --- | --- |
| `registry.json` | серверы, наборы, ключи, распределитель портов | `0600` |
| `registry.json.bak` | предыдущий `registry.json`, перезаписывается при каждом сохранении | `0600` |
| `registry.json.corrupt-<unix>` | реестр, который не разобрался, отложенный в сторону | как записан |
| `env.json` | переменные под управлением панели (плоские строки) | `0600` |
| `manager.pid` | pid работающей панели; вторая панель отказывается стартовать | `0600` |
| `sets/<id>/servers.json` | материализованная конфигурация, которую читает процесс набора | `0600` |
| `sets/<id>/mcprig.log` | лог набора; ротируется в `.log.1` при 5 МиБ | `0600` |
| `sets/<id>/stats/` | история вызовов и посуточные `raw-*.jsonl` этого набора | `0600` |
| `sets/<id>/tool-out/` | тела ответов, вырезанные из ответа, по файлу на каждый | `0600` |
| `sets/<id>/oauth.json` | токены доступа и обновления для OAuth-нижестоящих этого набора | `0600` |
| `sets/<id>/oauth-clients.json` | данные динамической регистрации клиентов этого набора | `0600` |
| `oauth.json` | токены доступа и обновления для OAuth-нижестоящих отдельного процесса `serve`/`stdio` | `0600` |
| `oauth-clients.json` | данные динамической регистрации клиентов отдельного процесса `serve`/`stdio` | `0600` |

Набор, запущенный панелью, получает `MCPRIG_DATA_DIR=<data>/sets/<id>`, поэтому
его OAuth-файлы лежат в `sets/<id>/`. Файлы `oauth*.json` в корне относятся к
процессам `serve`/`stdio`, запущенным с корневым каталогом состояния.

`registry.json`, `env.json`, `sets/<id>/servers.json` и OAuth-файлы пишутся
через временный файл и переименование, поэтому падение не оставляет
полузаписанного файла. Лог набора и история вызовов дописываются на месте
(`O_APPEND`, `0600`) и могут кончаться неполной последней строкой. Не копируйте
`registry.json`, пока панель работает.

## См. также

- [configuration.md](configuration.md) — `servers.json`, из которого живёт набор.
- [cli.md](cli.md) — `panel`, `doctor`, `warm` и флаги, с которыми юниты запускают бинарник.
- [dev/release.md](dev/release.md) — как собираются архивы релиза.
- [troubleshooting.md](troubleshooting.md) — когда юнит не поднимается.
