# Проверка замечаний пользователя по mcprig (2026-11-10)

Проверено: `master` `b94a3fd` (v0.3.7), образы реестра
`mcprig:0.3.7-slim` и `:0.3.7-full`,
живые контейнеры `docker run`, тестовый прогон инструментов в пакете
`internal/mcpserver`. Код не менялся.

## Итог

| № | Замечание | Вердикт |
| - | --- | --- |
| 1 | `list_commands` отдаёт `command`, `invoke_command` ждёт `command_name`; ошибка «лишние ключи: command» без подсказки | подтверждено |
| 2 | `sandbox: {}` в контейнере → `sandbox unavailable: bwrap не найден в PATH`; в slim и full bwrap нет | подтверждено; в контейнере нужны bwrap + `prlimit` + `--privileged` |
| 3 | в slim нет `npx`, stdio-серверы из npm не стартуют | подтверждено живьём; в `full` нет и `uvx` |
| 4 | панель в контейнере слушает 127.0.0.1, опубликованный порт молчит; лечится `MCPRIG_HOST=0.0.0.0` | подтверждено |
| 5 | тексты ошибок валидации не называют ожидаемую схему | подтверждено частично |

## 1. Рассинхрон `command` / `command_name` — подтверждено

- `internal/mcpserver/manager.go:450-454` — `commandInfo` отдаёт и `name`, и
  `command` (комментарий: «kept for older callers; same as name»).
- `internal/mcpserver/manager.go:628-633` + `:637` — `invoke_command` принимает
  только `server`, `command_name`, `parameters`, `maxBytes`.
- Живой ответ (тестовый клиент): `Error: лишние ключи: command — аргументы
  инструмента кладутся в parameters` (`manager.go:991`).
  Подсказка уводит в `parameters`, хотя правильное поле — `command_name`.
- Вторая половина той же ловушки: `describe_commands` ждёт `command_names`
  (массив, `manager.go:578`), `invoke_command` — `command_name`, а
  `list_commands` отдаёт `command`. Три разных имени для одного понятия.

Починка — одна строка: при `extra` == `command` говорить «команда вызывается
полем `command_name`»; в идеале принимать `command` как алиас.

## 2. Песочница в контейнере — подтверждено

- `Dockerfile:20` (full) и `:32` (slim) — `apk add ca-certificates [python3 py3-pip]`,
  bubblewrap не ставится.
- Проверка в запущенных образах: `command -v bwrap` → MISSING в обоих.
- Умолчания `sandbox: {}` — bwrap и `onUnavailable: deny`
  (`internal/config/config.go:93-95`), отказ `sandbox unavailable: %w`
  (`internal/sandbox/sandbox.go:107`), `%w` = `bwrap не найден в PATH`
  (`sandbox.go:42`). `mcprig doctor` в slim: `предупреждение: песочница: bwrap
  не найден в PATH`.
- Это заявлено в `docs/en/install.md:120-125` и `deploy/README.md:13-15`:
  ставить пакет в свой образ и не отключать userns. То есть поведение
  штатное, но пользователь узнаёт о нём только из доков.

Побочно: `mcprig doctor` печатает `ok: предупреждение: …`
(`cmd/mcprig/doctor.go:366`, префикс `ok: ` — `:153`), хотя это предупреждение.

## 3. slim без npx — подтверждено, плюс дефект в full

- slim: `command -v node npx npm` → все MISSING; ошибка старта —
  `exec: "npx": executable file not found in $PATH`.
- full: node/npx/npm есть.
- Но заголовок `Dockerfile:3` обещает «plus node and python, so npx/uvx-based
  stdio servers run here too», а `uv`/`uvx` в full MISSING (`:20` ставит
  `python3 py3-pip`, питон-лаунчера нет). Серверы на `uvx` не работают и в full.

## 4. Панель слушает loopback — подтверждено

- `internal/manager/api.go:61` — `MCPRIG_HOST`, по умолчанию `127.0.0.1`.
- В контейнере: `/proc/net/tcp` → `0100007F:2383` (127.0.0.1:9091);
  curl с хоста на опубликованный порт → `curl: (52) Empty reply from server`
  (то же, что Python `RemoteDisconnected`).
- С `MCPRIG_HOST=0.0.0.0` тот же контейнер → `/healthz` = 200.
- Та же ловушка у набора: `cmd/mcprig/main.go:245-250` — без `transport.host`
  слушает 127.0.0.1. В `deploy/sets/*/servers.json` стоит `0.0.0.0`, а вот
  панель в контейнере нигде не описана: `Dockerfile` объявляет только
  `EXPOSE 9100`, docs про контейнер говорят лишь о `serve`.

Нужна строка в доки («панель в контейнере: `MCPRIG_HOST=0.0.0.0`») и `EXPOSE 9091`.

## 5. Тексты ошибок валидации — подтверждено частично

Живые ответы:

```
A command вместо command_name → лишние ключи: command — аргументы инструмента кладутся в parameters
B server: 5                   → аргументы: json: cannot unmarshal number into Go struct field invokeCommandInput.server of type string
C без command_name            → command_name обязателен
D без server                  → server обязателен
E parameters: "x"             → parameters: ожидался объект
F describe_commands + command → лишние ключи: command
```

Что не так: типовая ошибка выносит наружу имя Go-структуры
(`invokeCommandInput.server`) и не говорит «ожидалась строка»; «лишние ключи»
не перечисляет допустимые; «обязателен» не говорит тип и назначение.
Хороший образец — `parameters: ожидался объект`.

Починка: разбирать `*json.UnmarshalTypeError` в сообщение
`server: ожидалась строка, получено число`; в «лишние ключи» добавлять
`допустимо: server, command_name, parameters, maxBytes`.

## Что ещё нашлось

- `Dockerfile:3` врёт про `uvx` (см. п.3).
- `ok: предупреждение:` в `doctor` (см. п.2).
- Одно понятие — четыре имени в API: `name`, `command` (ответ
  `list_commands`), `command_name`, `command_names`.
- Панель в контейнере: нет `EXPOSE 9091` и нет абзаца в доках (см. п.4).

## Допроверка: песочница и uvx живьём (2026-11-10, rootless Docker 29.9.3)

Свой образ `mcprig:sbx-test` = `0.3.7-full` + `apk add bubblewrap` +
`npm i -g @modelcontextprotocol/server-everything`; набор из двух серверов
(`boxed` с `"sandbox": {}` и тегом `untrusted`, `plain` без блока), вызовы
через `list_servers` и `invoke_command` по HTTP.

| состояние контейнера | что видно |
| --- | --- |
| штатный slim/full | `boxed: sandbox unavailable: bwrap не найден в PATH` |
| bwrap добавлен, docker по умолчанию | `sandbox unavailable: bwrap не создаёт namespace (userns выключен?): exit status 1: bwrap: No permissions to create a new namespace…` |
| `--security-opt seccomp=unconfined` | `boxed: sandbox: путь "prlimit" должен быть абсолютным` |
| + `apk add util-linux` (prlimit) | `bwrap: Can't mount proc on /proc: Operation not permitted` |
| `--privileged` | `boxed` healthy, `isolation: bwrap`, `list_commands`/`invoke_command` работают |

Корень второй строки — rootless Docker и запрет unprivileged userns; третья
строка — дефект образа/доков, не хоста (в alpine нет `prlimit`, см. ниже).
`--cap-add SYS_ADMIN`, `apparmor=unconfined`, `seccomp=unconfined` по
отдельности proc не разрешают; полностью песочница завелась только под
`--privileged`.

**uvx живьём.** В штатном `full`: `uvx-srv: exec: "uvx": executable file not
found in $PATH` (а `npx`-сервер рядом — `healthy: true`). `pip3 install uv` →
PEP 668 (`externally-managed-environment`); `pip3 install --break-system-packages
uv` не от root кладёт `uvx` в `~/.local/bin` (не в PATH → сервер по-прежнему не
стартует); от root — `/usr/bin/uvx`. В образе с `uv` сервер `uvx mcp-server-time`
поднялся: `healthy: true`, `get_current_time` вернул
`{"datetime":"2026-11-10T14:37:41+00:00","day_of_week":"Tuesday"}`.

## Что ещё нашлось (допроверка)

- **Маркер изоляции врёт ровно в отказе.** `list_servers` отдаёт
  `"isolation": "bwrap"` серверу, который не стартовал из-за недоступной
  песочницы (`internal/mcpserver/manager.go:1151-1166`: `sandboxNote`
  заполняется только на путях `warn`/`allow`, а `deny`-отказ его не ставит).
  Это главная фича v0.3.7 — и в самом важном случае она показывает не то.
- **Песочнице в alpine нужен `prlimit`.** Его нет ни в busybox, ни в образах;
  `internal/sandbox/sandbox.go:180-185` при отсутствии `LookPath("prlimit")`
  оставляет строку `"prlimit"`, и bind падает с `sandbox: путь "prlimit" должен
  быть абсолютным`. В `docs/en/configuration.md:111` сказано обратное
  («`prlimit` монтируется сам, дело не в библиотеке») — для alpine это неверно.
  Предпосылка не описана ни в `install.md`, ни в `deploy/README.md`.
- **`mcprig doctor` даёт ложный зелёный.** В образе без `prlimit` он пишет
  `ok: песочница: bwrap запускается, userns разрешён`, хотя каждый сервер с
  `sandbox` падает на `prlimit` (`probeRun`, `sandbox.go:39-60`, этот путь не
  проверяет).
- **`full` без `uvx`.** `Dockerfile:3` обещает uvx-серверы, `:20` ставит только
  `python3 py3-pip`; `uv` нужен отдельно (см. выше).

## Как проверялось

```bash
# образы
docker run --rm --entrypoint sh mcprig:slim -c 'for c in node npx npm bwrap uv uvx python3; do command -v $c || echo MISSING; done'
docker run --rm --entrypoint /usr/local/bin/mcprig ...:0.3.7-slim doctor
# панель в контейнере
docker run -d -p 19091:9091 -e MCPRIG_PASSWORD=x ...:0.3.7-slim panel
curl -sS -m 3 http://127.0.0.1:19091/healthz          # Empty reply
docker exec <c> grep :2383 /proc/net/tcp              # 0100007F = 127.0.0.1
# тексты ошибок — тест в internal/mcpserver поверх штатных хелперов, удалён после прогона
go test ./internal/mcpserver -run TestZZProbeMessages -v
```
