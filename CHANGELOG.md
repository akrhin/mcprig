# Changelog

Notable changes to mcprig, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), versions follow
[SemVer](https://semver.org/). Releases are cut from tags: Gitea is the source
of truth, GitHub (`akrhin/mcprig`) is a public mirror with the same archives.

## [Unreleased]

### Fixed

- Отказ правами внутри `invoke_many` несёт в `result` тот же маркер
  `🚫 Blocked by mcprig policy: …` и `_meta.lazyMcpPolicy: "blocked"`, что и
  `invoke_command`: хук-клиент видит отказ в веере, а не только строку `error`.
- Усечение ответа с `structuredContent` добавляет к маркеру строку
  `[structuredContent не влезает в maxBytes: полный ответ в файле]` — видно, что
  структура отброшена и лежит в выгруженном файле.
- При обоих пустых именах `invoke_command` и `invoke_many` говорят
  `укажите command_name или command` вместо `command_name обязателен`.
- `list_servers`: проба, не дождавшаяся свободного слота, больше не помечает
  сервер `healthy: false` на 15 секунд — исход неизвестен, ответ
  `healthy: true, unknown: true` без `error` и без записи в кэш. Панель
  показывает у такого сервера «не проверено», а не «health ok».
- Панель получила `IdleTimeout` 60 с; `WriteTimeout` не ставится — долгие
  стримы ответов не должны обрываться.

## [0.3.8] - 2026-11-10

### Added

- `invoke_command` и `invoke_many` принимают `command` синонимом
  `command_name` — так поле называет `list_commands`; оба ключа с разными
  значениями отвергаются.
- `CONTRIBUTING.md` and `CONTRIBUTING.ru.md`: where an issue and a pull request
  go, the commit form, the gate before pushing, and the rule that a behaviour
  change updates both doc trees and the changelog.
- `.gitea` issue forms for a bug and a proposal, plus a pull-request template
  that asks what changed, the gate result and the docs touched.
- Versioning rules in both contributing files: SemVer pre-1.0, the tag is the
  only version source, what a `PATCH` and a `MINOR` may carry, and how a
  release commit and a published tag are treated.

### Fixed

- Схемы инструментов больше не объявляют `command_name` обязательным: он и
  `command` — равноправные имена (JSON Schema `anyOf`), поэтому клиент,
  проверяющий `required`, принимает `command`.
- `invoke_many` доведён до общего стиля ошибок: тип значения — «ключ command:
  ожидался тип «строка», получен тип «число»» с индексом вызова, лишний ключ
  перечисляет допустимые, подсказка упоминает синоним.
- Топ-level аргументы не-объект переводятся в «аргументы: ожидался тип
  «объект», получен тип «строка»», без имени Go-типа.
- Ошибка лишних ключей перечисляет допустимые, а ошибка типа называет ключ и
  вид значения (`ключ server: ожидался тип «строка», получен тип «число»`)
  вместо имени Go-структуры.
- Причина изоляции не дублирует префикс: `без изоляции: bwrap не найден в
  PATH`, а не `без изоляции: sandbox unavailable: …`.
- `doctor` проверяет `prlimit` и печатает предупреждения как `внимание:`, не
  `ok:`.
- Документация: `command` в таблице `invoke_command` (en/ru), сборка образа с
  `--build-arg VERSION=` (иначе бинарь отвечает `dev`).
- Образы везут `bubblewrap` и `prlimit` (`util-linux`): `"sandbox": {}` не
  падает на `bwrap не найден в PATH`, а песочница больше не отвергает
  `prlimit` как относительный путь.
- Образ `full` везёт uv/uvx в `/usr/bin`: stdio-серверы через `uvx` стартуют.
- Оба образа задают `MCPRIG_HOST=0.0.0.0`: панель в контейнере отвечает на
  опубликованном порту, а не только на loopback внутри.
- Ошибка сервера больше не обещает `bwrap` тому, кому песочница не досталась:
  `list_servers` показывает реальную изоляцию.

## [0.3.7] - 2026-11-10

### Added
- Panel shows each server's isolation: a muted `bwrap` badge when the sandbox
  applied, a warning `без изоляции` badge when bubblewrap did not start, and
  `конфиг отвергнут` for a record with `_configError`.

## [0.3.6] - 2026-11-10

### Added

- Панель редактирует реестр серверов хоста: `GET/POST /api/servers`,
  `PUT/DELETE /api/servers/<имя>` и форма в дровере с полями транспорта,
  `env`/`envAllow`, тегов и блока `sandbox` (`mode`, `onUnavailable`, `ro`,
  `rw`, `network`, `memoryMb`, `pids`, `tmpMb`, `timeoutSec`). Отказ бэкенда
  показывается в форме, запись не меняется.
- Мок панели считает статистику по фильтрам и заполняет счётчики всех наборов
  из `bySet`.
- `mcprig stdio` ведёт историю вызовов: `$MCPRIG_DATA_DIR/stdio/stats/` держит
  те же `raw-*.jsonl`, `day-*.json` и `month-*.json`, что каталог набора, и
  разрез по периодам читается с диска. Клиент в строке — `stdio`: транспорт и
  есть единственный клиент процесса. Недоступный каталог — предупреждение в
  stderr, прокси обслуживает дальше.
- `GET /api/stats?set=all` (и пустой `set`) отдаёт `bySet`:
  `{set, calls, failures, totalMs}` по наборам, у которых в периоде есть
  вызовы, — таблица наборов показывает счётчики, не открывая набор. Набор без
  вызовов в разрез не попадает, нулями не отвечает; у одного набора `bySet` нет.

### Changed

- `period=7d` держит в памяти распределение латентностей (значение мс → сколько
  вызовов), а не список всех вызовов: память зависит от числа разных значений
  латентности, а не от числа строк журнала. Процентили остались точными
  (`approximate:false`).
- Просмотр набора показывает `logging` по наличию инструмента
  `logging/setLevel` в `tools/list`, а не по `ServerCapabilities.Logging`
  (deprecated с ревизии протокола 2026-07-28, SEP-2577). Где инструмент есть,
  вид и поведение прежние.

### Fixed

- `prlimit` с динамическим загрузчиком монтируется в песочницу сам (как
  `/lib`, `/lib64`, `/usr/lib`, `/usr/lib64` при наличии), поэтому явный `ro`
  без `/usr` больше не ломает старт сервера с лимитами.
- `mcprig stdio` чистит `tool-out` (`MCPRIG_TOOLOUT_TTL`/`MCPRIG_TOOLOUT_MAX`)
  тем же свипом при старте и раз в час, что `serve` и `panel`, и в том же
  базовом каталоге: выгрузки stdio-процесса больше не остаются навсегда.

## [0.3.5] - 2026-11-10

### Added

- История вызовов на диске у каждого набора (`sets/<id>/stats`, у standalone
  `serve` — рядом с конфигом): `raw-YYYY-MM-DD.jsonl` по строке на вызов,
  `day-YYYY-MM-DD.json` с суточной агрегацией и фиксированной гистограммой
  (1…10000 мс, +∞), `month-YYYY-MM.json` для месяцев старше 30 суток. Ретенция:
  raw — сегодня + 7 суток, day — до 30, дальше только месяц; ротация и уборка
  при старте набора и раз в сутки. Запись идёт через ограниченный канал и одна
  горутина: вызов инструмента не ждёт диска, полный канал теряет строку и
  считает её в `stats.dropped` снимка набора. Файлы `0600`, каталог `0700`.
- `GET /api/stats?set=<id|all>&period=today|7d|30d|archive&server=&tool=&client=&bucket=hour|day|month`:
  итоги, ряд по времени и разрезы по инструментам, серверам и клиентам. За
  ≤7 суток процентили считаются по сырому журналу точно (`approximate:false`),
  за 30 суток и архив — по гистограмме (`approximate:true`, середина корзины).
  `set=all` складывает все наборы; набор без каталога истории — в `unreachable`.
  Неизвестный `bucket` — `400`.

### Changed

- Лог набора теперь `sets/<id>/mcprig.log` (был `lazy-mcp.log`). При первом
  старте набора унаследованный `lazy-mcp.log` переименовывается, хвост не
  теряется; формат `servers.json` не изменился.
- Панель пишет в свой stdout (его забирает systemd) старт и остановку набора и
  переход сервера в нездоровье: `набор <id>: запущен|остановлен`,
  `набор <id>: сервер <имя> unhealthy` с `reason`. Нездоровье логируется только
  при смене состояния, не на каждом цикле опроса health; состояние опрашивается
  фоном раз в 30 с, поэтому строки есть и без открытой панели.
- При старте панель сворачивает `day-*.json` в месячные файлы у всех наборов:
  набор, остановленный навсегда, больше не теряет свои дни из архива. Архив
  также читает несвёрнутые `day-*.json` старше 30 суток.
- Строка `остановлен` пишется общим путём `Stop`/`Restart`: рестарт её больше не
  пропускает, а `Stop` после падения процесса её не выдумывает.

### Fixed

- Запись с сессионной cookie, но без `Origin`, больше не проходит: при
  cookie-аутентификации `POST`/`PUT`/`DELETE` требуют `Origin`, совпадающий с
  запросом целиком — схема, хост и порт (`403` «нужен заголовок Origin»).
  `MCPRIG_ALLOWED_HOSTS` источником Origin больше не является: панель на чужом
  порту того же хоста не может остановить набор. За TLS-прокси исходная схема и
  хост берутся из `X-Forwarded-Proto`/`X-Forwarded-Host`, а `MCPRIG_PROXY_SECRET`
  считается доверенным прокси и без них (`403` «origin не совпадает с хостом»
  остаётся для чужого origin). `POST /api/login` не затронут.
- Неизвестный `command_name` теперь отвергается по кэшу инструментов сервера
  до вызова апстрима, с одним текстом `неизвестный инструмент "x" у сервера "y"`;
  прежние три варианта (`unknown tool %q`, `rejected by transport`, ответ
  апстрима) больше не расходятся. Пустой кэш — вызов идёт как раньше.
- Отвергнутый на загрузке сервер больше не обещает `isolation: bwrap` в
  `list_servers` и health: поле отвечает `нет: конфиг отвергнут`.
- `prlimit`, через который песочница ставит лимиты, монтируется вместе со
  своим загрузчиком и libc: явный `ro` без `/usr` больше не даёт
  `bwrap: execvp /usr/bin/prlimit: No such file or directory`.
- Битый `day-*.json` больше не блокирует свёртку месяца и уборку raw: файл
  пропускается с предупреждением в журнале набора. Ошибка переименования
  `lazy-mcp.log` → `mcprig.log` тоже пишется предупреждением, а не глотается.

- Проба песочницы шла через `/bin/true` (симлинк): при `PATH` с `/bin` впереди
  пробный запуск падал «exec внутри песочницы не удался», и с
  `onUnavailable: deny` ни один изолированный сервер не стартовал. Теперь путь
  разворачивается по симлинку, а `/bin` привязан в пробе.
- `prlimit` запускался по имени из PATH ребёнка: без `/usr` в `ro` падало
  `bwrap: execvp prlimit: No such file or directory` без подсказки. Теперь
  абсолютный путь, а `doctor` предупреждает о `sandbox.ro` без `/usr`.
- Умолчание `sandbox.memoryMb` поднято с 512 до 2048: под 512 не стартовал не
  только node, но и любой Go-сервер (`failed to reserve page summary memory`).
- Опечатка в имени поля `sandbox` (`memoryMB`, `cwd` внутри блока) больше не
  молчит: `config.Load` и панель отвечают «sandbox: неизвестные поля: …».
- `sandbox.mode=none` с лимитами в блоке теперь даёт предупреждение — раньше
  блок выглядел настроенным, а лимиты не применялись.

## [0.3.4] - 2026-11-10

### Added

- Sandbox for untrusted stdio servers: `sandbox` block on a server
  (`mode: bwrap`, `onUnavailable: deny` by default), running the child under
  bubblewrap — own mount tree, read-only root, no `/home`, no network, no
  `/proc/1/root` — with `prlimit` limits inside the namespace for address space
  and process count. Fail-closed: if bubblewrap cannot start, the server does
  not start either. `scripts/sandbox-e2e.sh` checks all of it against the host.
- Servers tagged `untrusted` require a sandbox; without one the config is
  rejected and the server never runs.
- `envAllow` per server, and a warning in `mcprig doctor` for `npx -y` / `uvx`
  without a pinned version, for an `envAllow` name the environment does not
  have, and for `memoryMb` below 2048 on a node command.
- `sandbox.tmpMb` (default 128): the `/tmp` tmpfs is size-limited and carries
  `RLIMIT_FSIZE`, so a child cannot fill the host disk. `mcprig doctor` probes
  bubblewrap itself.
- `list_commands` takes `namesOnly`: an array of tool names instead of the full
  catalog (arr-status: 89 KB and 443 tools), plus the document-everywhere
  `refresh` in its schema. A multi-part text answer is now joined with newlines
  instead of being wrapped in JSON, so the cut marker points at readable text.
- tool-out is swept every 10 minutes instead of hourly: a chatty server writes
  about 2 MiB an hour, and the old cadence let the 256 MiB cap overshoot.
- `list_servers` and the panel health answer carry `isolation`: `bwrap` when
  the sandbox is applied, `без изоляции: <reason>` when it was requested but
  unavailable. The note clears once the sandbox works again.

### Fixed

- A stdio server started by `mcprig serve` or `mcprig stdio` received the whole
  environment of the host process, including every API key in it. Now it gets a
  fixed base (`PATH`, `HOME`, `LANG`, `TMPDIR`, `LC_*`, `XDG_*`, `SSL_*`,
  `TZ`, `NODE_EXTRA_CA_CERTS`) plus what `env` and `envAllow` name explicitly.
- Editing a server in the panel dropped `sandbox` and `envAllow`: the registry
  parser did not read them, so the API answered `200` and the isolation
  disappeared on the next save. Both round-trip through add, update, import and
  export now.
- `untrusted` accepted `onUnavailable: warn|allow`, which is the same
  unprotected start only quieter; only `deny` passes.
- The `probe` and `browse` buttons, and `mcprig warm`, started a server without
  the validation `config.Load` applies, and `warm` still handed the child the
  whole host environment.
- A sandbox that failed to apply was visible only in the package log; `warn`
  and `allow` made the server run unprotected while health said it was fine.
- When a child died before `initialize`, the reason in its stderr was lost:
  Go sends stderr to `/dev/null`, so `bwrap: execvp …: No such file` arrived as
  a bare `EOF`. The last 4 KB of stderr now accompanies the error, with the
  server's own secrets masked.
- A `timeoutSec` that fired left the session alive, so the next call could read
  the previous answer; the session is dropped and the error unwraps to
  `context.DeadlineExceeded`.
- `probe` hard-coded `/usr/bin/true` and ran on every connect; it now resolves
  `true` through `PATH` and caches a success.
- A missing `rw`/`cwd` path was silently skipped, leaving the server without the
  directory it was meant to have; only missing default `ro` paths are skipped.
- `Check` filled defaults in the shared registry object, racing with set
  building; it now validates a copy.

## [0.3.3] - 2026-11-10

### Added

- Panel: six colour schemes — mcprig dark (default), light, Nord, Gruvbox,
  Solarized, Tokyo Night. Chosen in the header, remembered in `localStorage`,
  the first visit follows `prefers-color-scheme`. Themes are plain classes and
  a separate `themes.css`, so the panel CSP is unchanged (no inline styles).
- `POST /api/sets` returns the issued set token once in the `201` body; lists
  and the registry view still never repeat it.

### Fixed

- Tool answers are scrubbed of server secrets before counters and `tool-out`:
  values of `env`/`headers` with key/token/secret/pass/auth/credential in the
  name are replaced with `***`, so upstream error texts no longer leak API keys.
- Panel health beats the set's own `healthTimeout`: the load budget is 30 s
  against 15 s per server, so a restarted set no longer shows
  `context deadline exceeded` for the whole set.
- `POST /api/login` answers `400` for empty fields instead of a misleading `401`.

## [0.3.2] - 2026-11-09

### Changed

- Releases are mirrored to GitHub: `.github/workflows/release.yml` builds the
  same archives (goreleaser `--skip=publish`) and uploads them with `gh`.
- The goreleaser install step in both workflows fails loudly when `checksums.txt`
  or the archive could not be downloaded, instead of dying inside `sha256sum`.

### Fixed

- The stdio handshake now uses `initialize` (protocol `2025-11-25`), not
  `server/discover`: Python MCP SDK ≤ 1.9 fails to parse the new method, stops
  reading, and the client waited out its 15 s budget — qbittorrent in the `ha`
  set showed `context deadline exceeded`. HTTP sets keep the modern handshake.

## [0.3.1] - 2026-11-09

### Added

- Docker images in the Gitea registry: `0.3.1-slim`, `0.3.1-full`, and
  `latest`/`latest-full` following the release. `--build-arg VERSION` bakes the
  version into the binary, so `/healthz` reports the release, not `dev`.

### Fixed

- `tool-out` dumps are cleaned by TTL (24 h) and a 256 MiB cap.
- `mcprig doctor` reads the panel's environment before reporting, so its port
  and secrets are seen from the panel side.

## [0.3.0] - 2026-11-09

### Added

- `invoke_many`: up to 8 independent calls in one request.
- `list_commands` can be filtered by prefix; `invoke_command` caps large answers
  (`maxBytes`) and writes the full text to `tool-out`.
- Live tool-list refresh and one safe retry per call.
- `mcprig doctor` (health report) and `mcprig warm` (cache warm-up) with a
  systemd timer.
- Panel: client label from request `_meta`, schema weight counted in tokens.

### Fixed

- `${VAR}` resolution is parallel (up to 8 at once); logins queue one scrypt at a
  time; a busy explicit port is rejected on set creation; `goreleaser check`
  runs before the release job publishes.

## [0.2.0] - 2026-11-09

### Added

- Secrets from an OpenBao-backed store with a 30 s cache; `DELETE /api/env`
  reports where the value came from.
- Hardening from the paladin/arbiter review: cross-host redirects with
  `Authorization` are refused, the login limiter counts the client address
  rather than a proxy-appended one, logs are capped at 5000 lines, `{file:…}`
  is rejected in env and headers, `store=local` accepted as an alias, a string
  `command` rejected on import, sets polled in parallel in `/api/calls`.

## [0.1.0] - 2026-11-09

### Added

- First release: one binary that puts a whole MCP set behind 5 meta-tools
  (`list_servers`, `list_commands`, `describe_commands`, `invoke_command`,
  `invoke_many`) instead of hundreds of tools in the prompt.
- Registry and web panel over the same sets: add servers, groups, keys, call
  tail, per-set logs, env and secrets.
- HTTP and stdio transports; `init` prints client configs.
- Docker images (slim and full), Gitea Actions release with goreleaser and
  govulncheck, Go 1.27.3.
