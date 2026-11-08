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
выставлен в `docker-compose.yml`. Образы здесь **не собирались**: реестр
`docker.io` на этом хосте недоступен (`DeadlineExceeded` на
`load metadata for node:22-alpine`), так что сборку надо проверить там, где
registry доступен.

Юниты проверены `systemd-analyze verify`: ошибок нет, кроме отсутствующего по
пути `/home/<user>/.local/bin/mcprig` — он появится после установки бинарника.
