<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="design/mcprig/logo-hand-dark.png">
    <img src="design/mcprig/logo-hand-light.png" width="220" alt="mcprig">
  </picture>
</p>

# mcprig

Один бинарник: прокси для MCP-серверов, менеджер и веб-панель. Заводите серверы,
собираете из них наборы, каждый набор — отдельный процесс прокси на своём порту
со своим ключом.

Имя — «rig»: стенд, на который смонтированы все серверы. Свободно в npm, PyPI,
GitHub, crates.io, `.dev` и `.io` (проверено 2026-11-08).

## Откуда взялось

Проект вырос из [lazy-mcp](https://gitlab.com/gitlab-org/ai/lazy-mcp) (MIT) —
прокси, который отдаёт четыре мета-тула (`list_servers`, `list_commands`,
`describe_commands`, `invoke_command`) вместо того, чтобы грузить в контекст
сотни инструментов сразу. Идея, формат конфига и сам контракт мета-тулов взяты
оттуда; `servers.json` читается как есть, без миграции.

Прототип веб-панели, на котором проверялись сценарии и который мы переносим,
сделан [V0rt](https://github.com/V0rt).

## Чем отличаемся

| | lazy-mcp (TypeScript) | mcprig (Go) |
|---|---|---|
| рантайм | Node 20+ и 446 npm-пакетов | один статический бинарник |
| холодный старт | 95–137 мс | 12–44 мс (замер на этом хосте) |
| поставка | npm, PyPI (влезает `nodejs-wheel`), crates.io с prebuilt bun, Homebrew | бинарник под linux/darwin × x64/arm64 |
| состав | только прокси | прокси + менеджер + панель в одном процессе |
| валидация конфига | `env:{"A":1}` принимается, дальше панель падает на `text.matchAll` | типы проверяются на входе, отказ 400 с внятным текстом |
| токены | `GET /api/sets` отдаёт сырые 48-hex ключи | ключ виден один раз при выдаче, дальше только префикс |
| порты наборов | 1024–65535 | 9100–9199, как обещано в интерфейсе |
| протокол MCP | 2025-era + stateless fallback | 2026-07-28, stateless на каждый запрос, со фолбэком на старые версии |

Формат `servers.json` совместим: массив `servers[]`, `transport{type,host,port,
path,authToken,allowedHosts}`, `healthMonitor`, `requestTimeout`, обе формы
`command` (массив и legacy `command` + `args`). Невалидный сервер не валит
загрузку — попадает в список как unhealthy с текстом ошибки.

## Статус

Этапы 0–4 приняты: чтение `servers.json`, коннект по stdio и HTTP, четыре
мета-тула с permissions, HTTP-транспорт набора (stateless, 2026-07-28 с
фолбэком), клиентский OAuth для серверов с 401/403, менеджер (супервизия
наборов, HTTP API по контракту, счётчик вызовов). В работе: панель и
дашборд — этап 5.

Этапы и ворота — `docs/plan.md`, что делается сейчас — `docs/CURRENT-GOAL.md`.
Контракт, который переносим с TS-версии, — `docs/reference/ts-manager-contract.md`.

## Установка и подключение

Три способа, подробно — `docs/decisions/0002-installation.md`.

**Сервис (HTTP).** Один набор — один юнит `mcprig-set@<набор>.service`,
конфиг в `~/.config/mcprig/sets/<набор>/servers.json`, ключ в env-файле рядом.
Клиент подключается к `http://127.0.0.1:9100/mcp` с `Authorization: Bearer …`.

**Контейнер.** Для наборов из HTTP-серверов; stdio-серверы внутри образа есть
только те, что мы положили (образ `full` с node и python).

**Клиент.** `mcprig stdio --config <servers.json>` — клиент запускает бинарник
сам, ни порта, ни ключа. Для opencode:

```jsonc
// ~/.config/opencode/opencode.jsonc
"mcp": { "servers": { "demo": {
  "type": "local",
  "command": ["mcprig", "stdio", "--config", "/home/me/.config/mcprig/sets/demo/servers.json"]
} } }
```

Для общего процесса на несколько клиентов — `serve` и `type: "remote"` с
`headers.Authorization`. Ключ выдаёт панель или реестр; в API и логах он есть
только префиксом.

Секреты в `servers.json` не пишем: `${VAR}` раскрывается из окружения набора,
а разворачивание — `docs/reference/ts-manager-contract.md`.

## Сборка

```bash
go build ./...
go test ./...
go build -o mcprig ./cmd/mcprig
./mcprig -version
```

Нужен Go 1.25 (модуль просит 1.25.0, `GOTOOLCHAIN=auto` подтянет сам).

Лицензия — MIT (см. [LICENSE](LICENSE)), как у проекта-вдохновителя.
