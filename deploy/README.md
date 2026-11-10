# deploy/

Файлы для двух способов установки из `docs/decisions/0002-installation.md`.

| файл | что это |
| --- | --- |
| `mcprig-set@.service` | шаблонный юнит: один набор на порту из `servers.json` |
| `mcprig.service` | панель: реестр, супервизия наборов (дашборд — этап 5) |
| `mcprig-warm.service`, `mcprig-warm.timer` | шаблонный прогрев набора раз в 10 минут: первый вызов клиента не платит за холодный кэш |
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

## Прогрев набора

`mcprig warm` загружает кэш инструментов набора: `list_servers`, затем один
`list_commands` без `refresh` (с `refresh` кэш, наоборот, сбрасывается).
Таймер ставится на **каждый autostart-набор** — `%i` здесь имя набора:

```bash
install -Dm644 deploy/mcprig-warm.service ~/.config/systemd/user/mcprig-warm@.service
install -Dm644 deploy/mcprig-warm.timer   ~/.config/systemd/user/mcprig-warm@.timer
systemctl --user daemon-reload
systemctl --user enable --now mcprig-warm@main.timer
systemctl --user list-timers 'mcprig-warm*'
```

Проверить, что прогрев идёт:

```bash
journalctl --user -u mcprig-warm@main -n 20    # строка «итог: 1 набор, N инструмент(ов), X мс»
systemctl --user start mcprig-warm@main.service # прогнать немедленно
```

Юнит берёт порт панели из `MCPRIG_PANEL_URL` (в шаблоне —
`http://127.0.0.1:9088`, переопределяется в `~/.config/mcprig/panel.env`), пароль
панели — из `~/.config/mcprig/env`, токен набора — из
`~/.config/mcprig/<набор>.token`; набор с `open: true` прогревается без токена.
Вручную то же самое: `mcprig warm --set main` (или `--all --panel <адрес>`).

## Compose

`docker-compose.yml` поднимает два набора: `demo` — удалённый сервер на образе
`slim`, `ops` — stdio-сервер через `npx`, ему нужен образ с node.

```bash
# образы уже в реестре Gitea: latest (slim), latest-full и теги релиза 0.3.5-slim/0.3.5-full
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:latest
docker compose up -d

# либо собрать локально и указать свои теги
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
