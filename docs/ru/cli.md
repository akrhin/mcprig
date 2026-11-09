Файл перечисляет все подкоманды и флаги с настоящими примерами и их выводом.

# CLI

У `mcprig` четыре подкоманды. Запуск без подкоманды равнозначен `mcprig serve`;
запуск только с флагами (`mcprig -config …`) — тоже `serve`.

| команда | назначение |
| --- | --- |
| `serve` | один набор как HTTP-точка MCP |
| `stdio` | один набор через stdin/stdout, запускается клиентом |
| `panel` | менеджер и веб-панель (реестр, наборы, ключи, хвост вызовов) |
| `init` | напечатать фрагмент конфигурации клиента; ничего не пишет |

Коды возврата: `0` после справки, `2` для ошибки командной строки (неизвестная
подкоманда, лишний позиционный аргумент, неизвестный клиент), `1` для ошибки
времени выполнения (нет конфигурации, порт занят, отказ стартовать без
аутентификации).

## serve

```bash
mcprig serve --config ~/.config/mcprig/sets/demo/servers.json
```

| флаг | по умолчанию | значение |
| --- | --- | --- |
| `-config` | `os.UserConfigDir()/mcprig/servers.json` (Linux: `~/.config/mcprig/servers.json`) | конфигурация набора |
| `-addr` | из `transport.host` и `transport.port`, иначе `127.0.0.1:9100` | адрес прослушивания; перекрывает конфигурацию |
| `-version` | | напечатать версию сборки и выйти |

`-addr` меняет только слушателя; `transport.path` (по умолчанию `/mcp`),
`authToken`, `allowAnonymous` и `allowedHosts` по-прежнему берутся из файла.
Пустой `authToken` — ошибка старта, если `allowAnonymous` не true.

Вывод идёт в stderr:

```text
time=2026-11-09T00:10:09.939+03:00 level=INFO msg="прокси слушает" addr=127.0.0.1:9184 servers=2
```

`servers` считает пригодные записи: включённые и без ошибки конфигурации.

Слушатель поднимается раньше, чем тронут хоть один нижестоящий сервер, и сразу
после этого все включённые серверы прогреваются в фоне: первый клиентский вызов
после прогрева отвечает за миллисекунды, а не ждёт распаковки одиннадцати
`npx`/`uvx`-серверов. `MCPRIG_PPROF=127.0.0.1:6060` открывает `net/http/pprof`
на этом адресе для любой подкоманды; по умолчанию выключен, наружу ничего не
выставлено.

## stdio

```bash
mcprig stdio --config ~/.config/mcprig/sets/demo/servers.json
```

| флаг | по умолчанию | значение |
| --- | --- | --- |
| `-config` | тот же дефолт, что у `serve` | конфигурация набора |

Ни слушателя, ни ключа нет: процессом владеет клиент, а граница — транспорт.
Прокси пишет сообщения MCP в stdout, логи — в stderr. `transport` в файле
игнорируется. Счётчики вызовов в этом режиме не собираются.

## panel

```bash
MCPRIG_PASSWORD='change-me' mcprig panel
```

Флагов нет. Всё задаётся окружением:

| переменная | по умолчанию | значение |
| --- | --- | --- |
| `MCPRIG_DATA_DIR` | `$XDG_STATE_HOME/mcprig`, иначе `~/.local/state/mcprig` | реестр, ключи, логи, токены OAuth |
| `MCPRIG_HOST` | `127.0.0.1` | адрес прослушивания панели |
| `MCPRIG_PORT` | `9091` | порт панели |
| `MCPRIG_CLI` | запущенный исполняемый файл | бинарник, запускаемый для каждого набора (`serve --config …`) |
| `MCPRIG_BIND_IP` | `0.0.0.0` | `transport.host`, записываемый в `servers.json` каждого набора |
| `MCPRIG_SEED_FILE` | пусто | JSON с `servers[]` и `sets[]`; используется, только когда реестр пуст; для закрытых наборов генерируются новые ключи |
| `MCPRIG_PASSWORD` | пусто | пароль панели |
| `MCPRIG_PASSWORD_HASH` | пусто | `scrypt$N$r$p$salt$hash`; если задан, простой пароль игнорируется |
| `MCPRIG_PROXY_SECRET` | пусто | общий секрет для прокси-аутентификации; значение короче 32 символов пишет предупреждение в лог |
| `MCPRIG_USER` | `admin` | имя для входа |
| `MCPRIG_USER_HEADER` | `Remote-User` | заголовок с именем пользователя от доверенного прокси |
| `MCPRIG_ALLOWED_HOSTS` | только loopback | список разрешённых `Host` через запятую |
| `MCPRIG_AUTH` | пусто | `off` отключает аутентификацию панели |

Режимы аутентификации выводятся из заданного:

| пароль | прокси-секрет | `MCPRIG_AUTH` | режим |
| --- | --- | --- | --- |
| любой | любой | `off` | нет |
| да | нет | | пароль |
| нет | да | | прокси |
| да | да | | прокси + пароль |

Без всего этого панель отказывается стартовать:
`API открыт: задайте MCPRIG_PASSWORD или MCPRIG_AUTH=off`.

`MCPRIG_BIND_IP=0.0.0.0` — дефолт для слушателей **наборов**, а не для панели.
Наборы защищены своими bearer-ключами; сама панель по-прежнему привязывается к
loopback, если `MCPRIG_HOST` не говорит иного.

```text
2026/11/09 00:05:27 WARN менеджер: MCPRIG_ALLOWED_HOSTS пуст — принимаются только loopback-хосты
2026/11/09 00:05:27 INFO менеджер запущен addr=127.0.0.1:9080 data=/tmp/opencode/docdata auth=none
```

Вторая панель на том же каталоге данных отказывается стартовать из-за
`manager.pid`.

## init

`init` печатает фрагмент для собственного файла конфигурации клиента. Сам он
этот файл никогда не правит.

```bash
mcprig init --client opencode --set demo --config ~/.config/mcprig/sets/demo/servers.json
```

| флаг | по умолчанию | значение |
| --- | --- | --- |
| `-client` | `opencode` | одно из `opencode`, `claude`, `codex` |
| `-set` | `demo` | имя набора в сгенерированном имени сервера (`mcprig-<set>`) |
| `-remote` | пусто | URL набора, например `http://127.0.0.1:9100/mcp`; пусто означает stdio |
| `-key` | пусто | bearer-токен для `--remote`; `-` читает stdin |
| `-key-file` | пусто | файл с bearer-токеном; предпочтительнее `-key`, чьё значение видно в `ps` и истории шелла |
| `-binary` | `mcprig` | путь к бинарнику для stdio-режима |
| `-config` | пусто | путь к `servers.json` для stdio-режима (обязателен) |

Приоритет ключа: `-key-file`, затем `-key` (`-` значит stdin), затем
`MCPRIG_KEY`.

### Вывод stdio

```bash
mcprig init --client opencode --set demo --config /tmp/demo/servers.json
```

```json
{
  "mcp": {
    "mcprig-demo": {
      "command": [
        "mcprig",
        "stdio",
        "--config",
        "/tmp/demo/servers.json"
      ],
      "enabled": true,
      "type": "local"
    }
  }
}
```

```bash
mcprig init --client claude --set demo --config /tmp/demo/servers.json
```

```json
{
  "mcpServers": {
    "mcprig-demo": {
      "args": [
        "stdio",
        "--config",
        "/tmp/demo/servers.json"
      ],
      "command": "mcprig"
    }
  }
}
```

```bash
mcprig init --client codex --set demo --config /tmp/demo/servers.json
```

```toml
[mcp_servers.mcprig-demo]
command = "mcprig"
args = ["stdio", "--config", "/tmp/demo/servers.json"]
```

### Вывод remote

```bash
mcprig init --client opencode --set demo --remote http://127.0.0.1:9100/mcp --key-file ./demo.key
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

Для `claude` корневой объект — `mcpServers`, а `type` — `http`; для `codex`
фрагмент — таблица `[mcp_servers.<name>]` с `url` и
`http_headers = { Authorization = "…" }`.

### Ошибки

| вход | код возврата | сообщение |
| --- | --- | --- |
| `--key` без `--remote` | 2 | `--key и --key-file нужны только с --remote: в stdio-режиме ключ не требуется` |
| `--remote` без ключа | 2 | `для --remote нужен ключ: --key, --key-file, --key - или MCPRIG_KEY` |
| stdio без `--config` | 2 | `для stdio-режима нужен --config (путь к servers.json набора)` |
| неизвестный клиент | 2 | `неизвестный клиент "nope"; доступны: opencode, claude, codex` |
| файл ключа отсутствует | 1 | `ключ: open /nope: no such file or directory` |

Справка CLI, диагностика и строки логов в этой сборке — на русском; в этом
документе они приведены как есть.
