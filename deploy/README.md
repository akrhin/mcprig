# deploy/

Файлы для двух способов установки из `docs/decisions/0002-installation.md`.

| файл | что это |
| --- | --- |
| `mcprig-set@.service` | шаблонный юнит: один набор на порту из `servers.json` |
| `mcprig.service` | панель: реестр, супервизия наборов (дашборд — этап 5) |
| `Dockerfile` | два таргета: `slim` (только бинарник) и `full` (плюс node и python) |
| `docker-compose.yml` | два набора, конфиги смонтированы read-only, состояние в томах |

Раскладка на хосте:

```
~/.local/bin/mcprig
~/.config/mcprig/sets/<набор>/servers.json
~/.config/mcprig/sets/<набор>/env          # 0600: transport.authToken и ${VAR}
~/.config/mcprig/panel.env                 # 0600: MCPRIG_PASSWORD, MCPRIG_PROXY_SECRET
~/.local/state/mcprig/                     # токены OAuth, реестр
```

```bash
install -Dm755 mcprig ~/.local/bin/mcprig
install -Dm644 deploy/mcprig-set@.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now mcprig-set@demo.service
```

В контейнере состояние — том на `/data`, поэтому `MCPRIG_DATA_DIR=/data` уже
выставлен в `docker-compose.yml`.

## Compose

`docker-compose.yml` поднимает два набора: `demo` — удалённый сервер на образе
`slim`, `ops` — stdio-сервер через `npx`, ему нужен образ с node.

```bash
docker build -t mcprig:slim .                    # 32 МБ, только бинарник
docker build --target full -t mcprig:full .      # 334 МБ, плюс node и python
MCPRIG_IMAGE=mcprig:slim MCPRIG_IMAGE_FULL=mcprig:full docker compose up -d
```

Проверено на этом хосте: оба контейнера `healthy`, `demo` отвечает
`list_servers`, `ops` отвечает `Echo: compose ok`. Что важно знать:

- Конфиги наборов лежат в `sets/<набор>/servers.json` и монтируются read-only;
  `sets/<набор>/env` — переменные для `${VAR}` внутри конфига, там же
  `MCPRIG_SET_TOKEN` (замени `change-me-before-exposing`).
- `transport.authToken` обязателен: без него `serve` не стартует
  (`пустой authToken — без авторизации набор не стартует`).
- Порт берётся из `transport.port` конфига, а не из команды: опубликованный
  порт и порт в конфиге должны совпадать. Healthcheck образа смотрит на 9100,
  поэтому у `ops` он переопределён.
- `read_only: true` требует `tmpfs: /tmp:rw,exec` и `HOME=/tmp` с
  `npm_config_cache=/tmp/npm`: `npx` скачивает и запускает бинарь из `/tmp`, а
  без `exec` падает с `mcp-server-everything: Permission denied`.

Юниты проверены `systemd-analyze verify`: ошибок нет, кроме отсутствующего по
пути `/home/<user>/.local/bin/mcprig` — он появится после установки бинарника.
