# 0002 — Как ставится mcprig

Статус: предложение. Дата: 2026-11-08.

## Что мы ставим

Один бинарник, три роли:

| роль | команда | что делает |
| --- | --- | --- |
| прокси | `mcprig serve --config <набор>/servers.json` | один набор на своём порту, 4 мета-тула по HTTP |
| stdio | `mcprig stdio --config <набор>/servers.json` | тот же набор, но транспорт stdio — бинарник запускает клиент |
| панель | `mcprig panel` | реестр, супервизия наборов, ключи, дашборд (этапы 4–5) |

Порт и ключ есть только у HTTP-режима. stdio не слушает ничего и никого не авторизует:
его жизнь равна жизни клиента.

## Вариант 1 — системный сервис, HTTP MCP

```
~/.local/bin/mcprig
~/.config/mcprig/sets/<набор>/servers.json   # конфиг набора, как у lazy-mcp
~/.config/mcprig/sets/<набор>/env            # 0600: transport.authToken + секреты
~/.local/state/mcprig/                       # токены OAuth, реестр панели
```

Шаблонный юнит `mcprig-set@.service`:

```ini
[Unit]
Description=mcprig set %i
After=network-online.target

[Service]
Type=simple
EnvironmentFile=%h/.config/mcprig/sets/%i/env
ExecStart=%h/.local/bin/mcprig serve --config %h/.config/mcprig/sets/%i/servers.json
Restart=on-failure
RestartSec=2
SyslogIdentifier=mcprig-%i

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now mcprig-set@demo.service
journalctl --user -u mcprig-set@demo -f
```

Когда панель готова, наборы ей больше не нужны как юниты: один `mcprig.service`
поднимает панель, а наборы она запускает дочерними процессами (как эталонный
менеджер). Шаблон остаётся для тех, кому панель не нужна.

**Почему это основной путь.** stdio-серверы (`npx`, `uvx`, локальные скрипты)
работают как есть; секреты берутся из env-файла с правами 0600; OAuth открывает
браузер на хосте; systemd даёт рестарт, лимиты и логи без нашего кода.

Минус: на каждый набор — свой юнит (или панель), а обновление бинарника требует
`systemctl --user restart`.

## Вариант 2 — контейнер

```bash
docker run -d --name mcprig-demo \
  -p 127.0.0.1:9100:9100 \
  -v ~/.config/mcprig/sets/demo/servers.json:/config/servers.json:ro \
  -v mcprig-state:/state \
  --env-file ~/.config/mcprig/sets/demo/env \
  frosty-ad-gt.cloudpub.ru/sintez/mcprig:latest serve --config /config/servers.json --addr 0.0.0.0:9100
```

`docker-compose.yml` для нескольких наборов отличается только именем сервиса,
портом и путём к конфигу.

Образ: `slim` (только бинарник, для HTTP-даунстримов) и `full` (плюс node и
python, чтобы `npx`/`uvx`-серверы запускались внутри). Healthcheck —
`/healthz` без авторизации, он уже есть.

**Где контейнер честно хуже.** stdio-даунстрим — это чужая программа. В контейнере
она либо лежит в образе, либо её нет; смонтировать хост целиком — это сломать
изоляцию, ради которой контейнер и берут. Поэтому контейнер — для наборов из
HTTP-серверов и для тех, кто и так живёт в Docker рядом с остальным стеком.

## Вариант 3 — установка в клиент (opencode, Claude Code, Codex)

Да, делать надо, и обеими формами: у них разные сценарии, а не разные вкусы.

**a. stdio — клиент сам запускает бинарник.** Ни порта, ни ключа, ни сервиса.
Процесс умирает вместе с клиентом; у каждого клиента свой. Так сегодня работает
lazy-mcp, и это самый короткий путь «попробовать».

```jsonc
// ~/.config/opencode/opencode.jsonc
"mcp": { "servers": { "demo": {
  "type": "local",
  "command": ["mcprig", "stdio", "--config", "/home/me/.config/mcprig/sets/demo/servers.json"]
} } }
```

**b. remote — клиент ходит в общий сервис.** Один процесс на все клиенты, ключ,
дашборд видит вызовы, OAuth-токены хранятся в одном месте.

```jsonc
"mcp": { "servers": { "demo": {
  "type": "remote",
  "url": "http://127.0.0.1:9100/mcp",
  "headers": { "Authorization": "Bearer ${MCPRIG_KEY_DEMO}" }
} } }
```

Клиенты, у которых конфиг другой: Claude Code — `~/.claude.json` (`mcpServers`),
Codex — `~/.codex/config.toml` (`[mcp_servers.demo]`), Cursor/Cline — свой файл.

**Команда-генератор** (этап 5): `mcprig init --client opencode --set demo [--remote]`
печатает готовый фрагмент, при `--write` вставляет его в конфиг клиента, сохраняя
бэкап и не трогая соседние ключи. Ключ берётся из реестра, а не выдумывается.

## Что выбирать

| ситуация | вариант |
| --- | --- |
| постоянная работа, stdio-серверы, свой ноутбук | сервис 1 |
| набор из HTTP-серверов, всё остальное уже в Docker | контейнер 2 |
| один клиент, хочется быстро | stdio в клиенте (3a) |
| несколько клиентов, нужен дашборд и один ключ | remote в клиенте (3b) + сервис 1 |

## Что это добавляет в код

| шаг | объём | этап плана |
| --- | --- | --- |
| `mcprig stdio` — тот же `mcpserver.Server()` на stdin/stdout | ~40 строк | 5 |
| `mcprig serve` — нынешний `main`, без изменений | — | есть |
| `mcprig init` — фрагменты конфигов четырёх клиентов | ~150 строк | 5 |
| `mcprig-set@.service` + пример env-файла | файл в репозитории | 6 |
| Dockerfile (slim/full), compose, healthcheck | ~60 строк | 6 |
| CI: goreleaser, multi-arch образ | workflow | 6 |
