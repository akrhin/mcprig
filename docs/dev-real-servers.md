Internal stand notes, RU only — not a product page.

# Стенд на настоящих MCP-серверах

Фикстура `cmd/testmcp` проверяет протокол, но не поведение: холодный `npx`,
LSP серены, индексация socraticode и поисковые индексы xerj ведут себя иначе.
Стенд поднимает панель и открытый набор с этими тремя серверами.

```bash
bash scripts/dev-real-set.sh          # поднимает панель и открытый набор; порты — дефолты скрипта (PANEL_PORT / SET_PORT)
bash scripts/dev-real-set.sh stop
```

Серверы в наборе:

| имя | тип | что проверяет |
| --- | --- | --- |
| `serena` | HTTP, `http://127.0.0.1:8899/mcp` | настоящий LSP: `activate_project`, `find_symbol`, символы в реальном репозитории |
| `socraticode` | stdio, `npx -y socraticode@1.16.1` | холодный старт npx, долгая индексация, продолжение работы после таймаута вызова |
| `xerj` | stdio, `~/.local/bin/xerj mcp` | локальный бинарь, 22 инструмента, поиск по индексам |

Вызовы идут через прокси, а не напрямую:

```bash
curl -s -XPOST http://127.0.0.1:${SET_PORT:-9101}/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"invoke_command","arguments":{"server":"serena","command_name":"find_symbol","parameters":{"name_path_pattern":"Connect","relative_path":"internal/downstream/connect.go"}}}}'
```

## Что стенд уже показал

- **Health-проба в 5 с врёт.** `serena`, `socraticode` и `xerj` при первом опросе
  получили `context deadline exceeded`, хотя все три работают: `serena`
  активировала проект с Go-LSP, `xerj` отдал 22 инструмента.
- **Долгий вызов обрывается, работа продолжается.** `socraticode.codebase_index`
  вернул `Error: таймаут вызова 120000 мс`, а через четыре минуты
  `codebase_status` показал `Full index in progress … Downloading Ollama Docker
  image`. Клиент видит ошибку и не знает, что ждать результат.
- **Первый вызов платит за холодный старт.** `npx -y socraticode@1.16.1`
  поднимается десятки секунд внутри запроса вызывающего.

## Ограничения стенда

- Набор открытый (без ключа) — так проще дергать руками; `GET /stats`
  у открытого набора отдаётся анонимно в редакции: без меток клиентов и текстов
  ошибок, но со счётчиками и латентностями, поэтому они видны в панели.
- `serena` держит один активный проект на процесс: параллельная работа другого
  агента перебьёт `activate_project`.
- Индексация socraticode тянет Docker-образ Ollama при первом запуске; без сети
  она не завершится.
