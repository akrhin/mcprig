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

Этап 1 из 6. Готово: чтение `servers.json` (`internal/config`), подключение к
downstream по stdio и HTTP (`internal/downstream`). В работе: мета-тулы
(`internal/mcpserver`), реестр менеджера (`internal/registry`), HTTP-транспорт
набора (`internal/httplisten`).

Этапы и ворота — `docs/plan.md`, что делается сейчас — `docs/CURRENT-GOAL.md`.
Контракт, который переносим с TS-версии, — `docs/reference/ts-manager-contract.md`.

## Сборка

```bash
go build ./...
go test ./...
go build -o mcprig ./cmd/mcp-manager
./mcprig version
```

Нужен Go 1.25 (модуль просит 1.25.0, `GOTOOLCHAIN=auto` подтянет сам).

Лицензия — MIT (см. [LICENSE](LICENSE)), как у проекта-вдохновителя.
