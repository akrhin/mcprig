<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="design/mcprig/logo-hand-dark.png">
    <img src="design/mcprig/logo-hand-light.png" width="220" alt="mcprig">
  </picture>
</p>

[Русский](README.ru.md) · [English](README.md)

# mcprig

Один статический бинарник на Go: отдаёт множество MCP-серверов через одну точку
входа, плюс веб-панель.

## Что это

mcprig — это три вещи в одном бинарнике:

- **прокси**, который выставляет четыре мета-инструмента (`list_servers`,
  `list_commands`, `describe_commands`, `invoke_command`) вместо загрузки всех
  нижестоящих инструментов в контекст клиента;
- **менеджер**, который хранит реестр серверов и наборов, следит за одним
  процессом на набор и выдаёт ключи доступа;
- **веб-панель** для того же реестра и живого хвоста вызовов.

**Набор** — именованная группа нижестоящих серверов, опубликованная как одна
точка MCP. Каждый набор работает своим процессом `mcprig serve` на своём порту
со своим bearer-ключом (или явно открыт), поэтому перезапуск или падение одного
набора не задевает остальные.

`servers.json` — формат lazy-mcp, читается как есть: `servers[]`, обе формы
`command`, `url`, `env`, `headers`, `transport`, `healthMonitor`,
`requestTimeout`. Сервер с битым полем остаётся в списке нездоровым со своей
ошибкой; отсутствующее имя или невалидный JSON валят загрузку.

## По сравнению с lazy-mcp

lazy-mcp — проект, из которого вырос mcprig; конфиг он понимает без правок.
Цифры ниже замерены на одном хосте против **того же** набора из одиннадцати
серверов (`serena`, `socraticode`, `xerj`, `open-design`, `context7`,
`tavily-mcp`, `playwright`, `raven`, `designer-skill`, `gitea`,
`portainer-mcp`, 421 инструмент) и тех же четырёх мета-инструментов.

| | mcprig 0.1.0 | lazy-mcp 2.10 (Docker, `node dist/cli.js`) |
| --- | --- | --- |
| RSS процесса-прокси после прогрева, 421 инструмент в кэше | **29 МиБ** | 67 МиБ |
| RSS до первого вызова | **13 МиБ** | — |
| старт до готового слушателя | **7–8 мс** | 95–137 мс (голый node) плюс entrypoint контейнера |
| `tools/list`, p50 | **0.9 мс** | 3.2 мс |
| `list_commands` по всем 11 серверам, p50 | **5.6 мс** | 10.9 мс |
| `list_servers` (health из кэша), p50 | **1.3 мс** | — |
| поставка | **один статический бинарник 12 МБ, без зависимостей** | образ 4.9 ГБ с node, npm и запечёнными серверами |
| падение набора | свой процесс, остальные наборы работают | весь прокси — один процесс |

Оба прокси не мешают: тёплый `list_commands` стоит меньше 10 мс, хотя ответ —
174 КиБ JSON, а 2 000 вызовов подряд оставляют RSS ровным (34 → 30 МиБ, GC
возвращает остальное).

Честные оговорки: одиннадцать нижестоящих серверов весят больше самого прокси —
тёплый набор держит node/python-процессы на ~1.5 ГиБ, из которых прокси — 29 МиБ.
Первый `list_commands` после старта занимает ~11 с: одиннадцать `npx`/`uvx`
серверов ещё поднимаются, и lazy-mcp платит ту же цену в том же месте. Health
кэшируется на 15 с, список инструментов — на время сессии: если нижестоящий
сервер меняет состав инструментов на живом соединении, клиент увидит старое до
переподключения.

## Быстрый старт

Нужен Go 1.27; `GOTOOLCHAIN=auto` скачает его при первой сборке.

```bash
# 1. сборка
go build -o mcprig ./cmd/mcprig

# 2. запуск панели (только loopback, в этой локальной демонстрации без входа)
MCPRIG_AUTH=off ./mcprig panel
# из другого шелла:
curl -s http://127.0.0.1:9091/healthz
# {"sets":0,"status":"ok"}
```

В панели добавьте сервер, добавьте набор с ним, нажмите **Start**, затем
выпустите ключ (**Keys** → issue). Токен показывается один раз; сохраните его в
`demo.key` (`chmod 600`). После этого напечатайте фрагмент конфигурации клиента:

```bash
# 3. подключение клиента
./mcprig init --client opencode --set demo \
  --remote http://127.0.0.1:9100/mcp --key-file ./demo.key
```

```json
{
  "mcp": {
    "mcprig-demo": {
      "enabled": true,
      "headers": {
        "Authorization": "Bearer <token>"
      },
      "type": "remote",
      "url": "http://127.0.0.1:9100/mcp"
    }
  }
}
```

`--client claude` печатает JSON `mcpServers` для `claude_desktop_config.json`;
`--client codex` печатает TOML для `~/.codex/config.toml`. Для stdio-клиента
(бинарник запускает клиент) передайте `--config <servers.json>` вместо
`--remote`. `init` ничего не пишет на диск.

![mcprig panel](design/ui/panel.png)

## CLI

| команда | что делает | пример |
| --- | --- | --- |
| `serve` | запускает один набор из `servers.json` как HTTP-точку MCP | `mcprig serve --config ./servers.json` |
| `stdio` | отдаёт один набор через stdin/stdout; процессом владеет клиент | `mcprig stdio --config ./servers.json` |
| `panel` | запускает менеджер и веб-панель | `MCPRIG_PASSWORD=... mcprig panel` |
| `init` | печатает фрагмент конфигурации клиента; файлов не пишет | `mcprig init --client claude --set demo --remote http://127.0.0.1:9100/mcp --key-file ./demo.key` |

Флаги, дословно:

| команда | флаги |
| --- | --- |
| `serve` | `-config` (по умолчанию `os.UserConfigDir()/mcprig/servers.json`), `-addr`, `-version` |
| `stdio` | `-config` (тот же дефолт) |
| `panel` | нет; настраивается переменными окружения `MCPRIG_*` |
| `init` | `-client` (по умолчанию `opencode`), `-set` (по умолчанию `demo`), `-remote`, `-key`, `-key-file`, `-binary` (по умолчанию `mcprig`), `-config` |

Флаги принимают одно или два тире (`-config` и `--config` — одно и то же). Коды
возврата: `0` для справки, `2` для ошибки командной строки, `1` для всего
остального. Подробности и примеры: [docs/ru/cli.md](docs/ru/cli.md).

## Конфигурация

`servers.json` — тот же файл, что читает lazy-mcp. Минимальный набор:

```json
{
  "servers": [
    {
      "name": "fs",
      "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/srv/docs"],
      "env": {"LOG_LEVEL": "warn"},
      "tags": ["files", "local"]
    },
    {
      "name": "serena",
      "url": "http://127.0.0.1:8899/mcp",
      "headers": {"Authorization": "Bearer ${SERENA_TOKEN}"},
      "tags": ["code"]
    },
    {
      "name": "db",
      "command": ["/usr/local/bin/db-mcp"],
      "env": {"DB_PASSWORD": "{file:/run/secrets/db_password}"},
      "enabled": false
    }
  ],
  "transport": {
    "type": "http",
    "host": "127.0.0.1",
    "port": 9100,
    "path": "/mcp",
    "authToken": "{file:/home/me/.config/mcprig/demo.token}",
    "allowedHosts": ["127.0.0.1", "localhost"]
  },
  "permissions": {
    "default": "allow",
    "rules": [
      {"server": "db", "pattern": "drop_*", "action": "deny", "message": "no destructive SQL"}
    ]
  },
  "requestTimeout": 120000
}
```

`${VAR}` берётся из окружения процесса набора; `{file:/absolute}` читает файл
секрета и срезает один завершающий перевод строки. `serve` отказывается
стартовать без `transport.authToken`, если только `transport.allowAnonymous` не
true. Полный справочник полей: [docs/ru/configuration.md](docs/ru/configuration.md).

## Мета-инструменты

| инструмент | что возвращает |
| --- | --- |
| `list_servers` | все серверы с `type`, `enabled`, `healthy`, тегами; необязательный `filter` по имени, описанию и тегам |
| `list_commands` | инструменты одного сервера (`server`) массивом или нескольких (`servers`) объектом с ключами по имени |
| `describe_commands` | входные схемы названных инструментов; неизвестные имена попадают в `missing` |
| `invoke_command` | выполняет `command_name` на `server`; аргументы нижестоящего инструмента кладутся в `parameters` |

Все четыре — тонкие обёртки над нижестоящими MCP-серверами. Формы ответов:
[docs/ru/meta-tools.md](docs/ru/meta-tools.md).

## Безопасность по умолчанию

| область | по умолчанию |
| --- | --- |
| bearer набора | сравнивается за постоянное время; пустой `authToken` отказывается стартовать, если набор не открыт явно |
| пароль панели | простой текст или `scrypt$N$r$p$salt$hash`; сравнение за постоянное время; 5 неудачных входов в минуту на IP, один scrypt за раз |
| сессия панели | cookie `mcp_session`: случайный id, подписанный HMAC-SHA256, TTL 12 ч, `HttpOnly`, `SameSite=Lax`, `Secure` поверх TLS; сессии живут в памяти, поэтому перезапуск разлогинивает всех |
| CSRF | записи отклоняют `Sec-Fetch-Site: cross-site`, требуют совпадения `Origin` с `Host` и JSON-тела |
| политика Host | пустые `allowedHosts` / `MCPRIG_ALLOWED_HOSTS` означают только loopback; `*` отключает проверку |
| токены | панель и API возвращают только 8-символьный префикс; полный токен показывается один раз, при выпуске ключа |
| `/stats` | у открытого набора метки клиентов становятся `(аноним)`, текст ошибок отбрасывается |
| тело запроса | ограничение 4 МиБ на точке MCP |

## Установка

Релизные архивы собирает `.goreleaser.yaml` для linux и darwin на amd64 и
arm64 (`mcprig_<version>_<os>_<arch>.tar.gz`, плюс `checksums.txt`).
Конфигурация написана; `goreleaser` на хосте разработки не установлен, поэтому
сама сборка архивов не проверена.

- юниты systemd (пользовательские): `deploy/mcprig.service` (панель) и
  `deploy/mcprig-set@.service` (один набор на юнит).
- Docker: `slim` (только бинарник, HTTP-нижестоящие) и `full` (добавляет node и
  python для stdio-серверов `npx`/`uvx`). Образы на этом хосте не собирались:
  реестр был недоступен.

Подробности: [docs/ru/install.md](docs/ru/install.md).

## Разработка

```bash
go build ./...
go vet ./...
go test ./... -race -count=1
E2E_BASE=http://127.0.0.1:9091 ./scripts/contract-e2e.sh
bash scripts/dev-real-set.sh
```

`dev-real-set.sh` собирает бинарник и поднимает панель и один открытый набор
против настоящих экземпляров serena, socraticode и xerj;
`bash scripts/dev-real-set.sh stop` гасит его. Больше:
[docs/ru/development.md](docs/ru/development.md).

## Статус

До 1.0, один оператор: одна панель, один пароль, без ролей на пользователя.
Этапы 0–4 приняты (конфигурация, ядро прокси, OAuth, HTTP-транспорт, менеджер);
этап 5 (панель) в работе. Справка CLI и строки логов в этой сборке — на русском.

Лицензия: MIT, см. [LICENSE](LICENSE).
