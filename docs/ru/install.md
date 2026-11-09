Файл охватывает релизные архивы, юниты systemd, Docker и состояние на диске.

# Установка

## Релизные архивы

`.goreleaser.yaml` собирает один статический бинарник на платформу:

| свойство | значение |
| --- | --- |
| ОС / архитектура | linux, darwin × amd64, arm64 |
| архив | `mcprig_<version>_<os>_<arch>.tar.gz` |
| содержимое | `mcprig`, `README.md`, `LICENSE`, `deploy/mcprig-set@.service`, `deploy/mcprig.service` |
| контрольная сумма | `checksums.txt` |
| релиз | черновик, changelog из git без коммитов `docs:` и `test:` |
| сборка | `CGO_ENABLED=0`, `-trimpath`, `-s -w -X main.version=<version>` |

Релиз публикуется дважды: в Gitea (источник истины — на тег там идут тесты,
`govulncheck` и `goreleaser`) и на GitHub в публичном зеркале `akrhin/mcprig`,
где `.github/workflows/release.yml` собирает те же архивы через
`goreleaser --skip=publish` и загружает их `gh`. Скачивать с
<https://github.com/akrhin/mcprig/releases>; любой тег можно выпустить и
руками: `gh workflow run release.yml -f tag=vX.Y.Z`.

`goreleaser` на хосте разработки не установлен, архивы собирает CI, а не он.
Собрать тот же бинарник руками:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags)" -o mcprig ./cmd/mcprig
```

Обычный `go build -o mcprig ./cmd/mcprig` даёт `dev` в `mcprig -version`.

## systemd (пользовательские юниты)

В `deploy/` лежат два юнита:

| юнит | что запускает | конфигурация |
| --- | --- | --- |
| `mcprig.service` | `mcprig panel` — реестр, надзор, панель | `~/.config/mcprig/panel.env` |
| `mcprig-set@.service` | `mcprig serve --config …` — один набор отдельной службой | `~/.config/mcprig/sets/<set>/servers.json` и `env` |

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

Оба юнита используют `Restart=on-failure`, `NoNewPrivileges=yes` и
`PrivateTmp=yes`. Панель останавливается с `TimeoutStopSec=15`, набор — с
`TimeoutStopSec=10`. `systemd-analyze verify` не сообщает ошибок, кроме пути к
ещё не установленному бинарнику.

## Docker

В `Dockerfile` две цели:

| цель | содержимое | нижестоящие |
| --- | --- | --- |
| `slim` (по умолчанию) | бинарник, корневые CA, пользователь без root `10001` | только HTTP |
| `full` | `slim` плюс node и python | stdio-серверы, запускаемые через `npx` / `uvx` |

Оба образа опубликованы в реестре Gitea проекта, так что сборку можно пропустить:

```bash
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:latest        # slim
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:latest-full   # full
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:0.3.3-slim    # закреплённый релиз
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:0.3.3-full
```

`latest` и `latest-full` идут за последним релизом, теги версии закрепляют пару
(`0.3.3-slim`, `0.3.3-full` — текущая).

Либо собрать самому:

```bash
docker build --build-arg VERSION=v0.3.3 -t mcprig:slim .
docker build --build-arg VERSION=v0.3.3 --target full -t mcprig:full .
docker run --rm -p 127.0.0.1:9100:9100 \
  -v "$PWD/servers.json:/config/servers.json:ro" \
  -e MCPRIG_DATA_DIR=/data \
  mcprig:slim
```

Точка входа образа — `mcprig`; команда по умолчанию —
`serve --config /config/servers.json`; адрес берётся из `transport.host` и `transport.port` — в конфиге должно стоять `0.0.0.0` и тот порт, который публикуешь. Рабочий каталог —
`/data`, владелец — uid 10001; смонтируйте туда том и задайте
`MCPRIG_DATA_DIR=/data`, чтобы токены OAuth и прочее состояние переживали
перезапуск. `slim` везёт healthcheck на `http://127.0.0.1:9100/healthz`; он рассчитан на порт
по умолчанию, при другом `transport.port` задай `--health-cmd` или healthcheck в
compose.

`deploy/docker-compose.yml` запускает два набора (`demo` на 9100, `ops` на 9101)
из `frosty-ad-gt.cloudpub.ru/sintez/mcprig:latest`, монтирует каждый `servers.json` только для
чтения и держит состояние в именованных томах.

Замерено на этом хосте: `docker build .` без `--target` даёт `slim` — 32 МБ,
бинарь плюс корневые сертификаты; `--target full` добавляет node и python и
весит 334 МБ. Оба образа запускались: `slim` отдал удалённую `serena` через сеть
хоста и ответил `find_symbol` через `invoke_command`, healthcheck — `ok`.

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
| `registry.json.corrupt-<unix>` | реестр, который не разобрался, отложенный в сторону | как записан |
| `env.json` | переменные под управлением панели (плоские строки) | `0600` |
| `manager.pid` | pid работающей панели; вторая панель отказывается стартовать | `0600` |
| `sets/<id>/servers.json` | материализованная конфигурация, которую читает процесс набора | `0600` |
| `sets/<id>/lazy-mcp.log` | лог набора; ротируется в `.log.1` при 5 МиБ | `0600` |
| `sets/<id>/oauth.json` | токены доступа и обновления для OAuth-нижестоящих этого набора | `0600` |
| `sets/<id>/oauth-clients.json` | данные динамической регистрации клиентов этого набора | `0600` |
| `oauth.json` | токены доступа и обновления для OAuth-нижестоящих отдельного процесса `serve`/`stdio` | `0600` |
| `oauth-clients.json` | данные динамической регистрации клиентов отдельного процесса `serve`/`stdio` | `0600` |

Набор, запущенный панелью, получает `MCPRIG_DATA_DIR=<data>/sets/<id>`, поэтому
его OAuth-файлы лежат в `sets/<id>/`. Файлы `oauth*.json` в корне относятся к
процессам `serve`/`stdio`, запущенным с корневым каталогом состояния.

Все записи идут через временный файл и переименование, поэтому падение не
оставляет полузаписанного файла. Не копируйте `registry.json`, пока панель
работает.
