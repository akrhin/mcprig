# Песочница для недоверенных MCP-серверов

Исследование к проектному решению. Дата: 2026-11-10. Область: mcprig, Linux-хост,
один бинарник, нижестоящие серверы — stdio (спавним мы) или HTTP (url+headers).

Постановка владельца: «Есть MCP недоверенные… его вначале хотелось бы запускать в
песочнице без прямого доступа к системе. Изоляция… проверять untrusted MCP сервера».

**Состояние на 2026-11-10:** этапы 0 и 1 реализованы в `f398872`; ниже они
описаны как замысел, фактические детали — в `docs/en/configuration.md` (Sandbox)
и коде `internal/sandbox`. Отличия от текста: режим `docker` не реализован
(только `none|bwrap`), `prlimit` стоит внутри bwrap, `cmd.Dir` не заменяется на
`--chdir`, а `doctor` делает настоящую пробу: запускает bwrap с userns и падает,
если песочницу просит конфиг (`cmd/mcprig/doctor.go:353-368`).

---

## 1. Как устроен путь вызова: где встаёт граница

Граница «наш процесс → чужой сервер» для stdio проходит ровно в одном месте —
`internal/downstream/connect.go`.

```
mcp.Client ──HTTP/stdio──▶ set process (mcprig serve) ──spawn──▶ stdio child
                                    └──HTTP──▶ remote MCP (песочница не применима)
```

| место | что происходит |
| --- | --- |
| `internal/downstream/connect.go:38` `Connect` | единственная точка входа в чужой сервер |
| `internal/downstream/connect.go:107-168` `transport` | развилка stdio/HTTP |
| `internal/downstream/connect.go:132` | stdio: `exec.Command(srv.Command[0], srv.Command[1:]...)`, `cmd.Dir = srv.Cwd` |
| `internal/downstream/connect.go:134-142` | `base := os.Environ()`; если `c.Env != nil` — whitelist; поверх — `flatten(srv.Env)` |
| `internal/downstream/connect.go:103` | `&mcp.CommandTransport{Command: cmd}` — процесс стартует внутри `client.Connect` (:48) |
| `internal/downstream/connect.go:104-124` | HTTP: `StreamableClientTransport`, заголовки через `withHeaders` (:111) |
| `internal/downstream/connect.go:157` `sameHostRedirect` | защита от уноса `Authorization` на другой хост |
| `internal/config/config.go:24-37` `Server` | `Transport` (:27), `Command` (:28), `Cwd` (:29), `Env` (:30), `URL` (:31), `Headers` (:32) |
| `internal/manager/runtime.go:479-512` `childEnv` | whitelist окружения процесса набора: `PATH, HOME, LANG, TZ, TMPDIR, NODE_EXTRA_CA_CERTS`, префиксы `LC_`, `XDG_`, `SSL_` (:481-490) |
| `internal/manager/runtime.go:190-194` | набор запускается как `exec.Command(cliPath, "serve", …)`, `Setpgid` (:191), env из `setEnv` |
| `cmd/mcprig/main.go:204`, `cmd/mcprig/main.go:285` | `&downstream.Connector{…}` **без поля `Env`** → `c.Env == nil` → вниз уходит весь `os.Environ()` |
| `internal/manager/browse.go:92`, `internal/manager/probe.go:28` | probe/browse передают `managerEnv` — там секретов менеджера нет |

Выводы по коду:

1. **Единственная точка внедрения изоляции** — `transport()` в
   `internal/downstream/connect.go:88`. Всё остальное (HTTP) либо не наше, либо уже
   ограничено `sameHostRedirect`.
2. **Песочница — это префикс argv**, а не новый транспорт: `bwrap … -- cmd args` и
   `docker run -i …` сохраняют stdin/stdout, поэтому `mcp.CommandTransport`
   продолжает работать без изменений.
3. **Окружение уже частично закрыто, но не по умолчанию.** Панель сужает env через
   `childEnv` (`runtime.go:479`), а `serve`/`stdio` (`main.go:204`, `main.go:285`)
   отдают ребёнку всё. Это первая дырка, которую надо закрыть независимо от песочницы.
4. `requestTimeout` (`internal/config/config.go:382`) — единственный существующий
   лимит; лимитов памяти, PID и числа процессов нет.

---

## 2. Модель угроз

Оценка «что даёт изоляция» — про песочницу процессов; часть сценариев она не лечит
принципиально.

| № | Сценарий | Что даёт песочница |
| --- | --- | --- |
| 1 | **Чтение секретов из env.** Пакет при старте выгребает `*_TOKEN`, `AWS_*`, `GITHUB_*` из `os.Environ()` и шлёт на свой хост | Полностью — если env не наследуется (whitelist) |
| 2 | **Кража файлов.** `~/.ssh/id_*`, `~/.aws/credentials`, `~/.config/gh`, cookies браузера | Полностью — если home не смонтирован |
| 3 | **Чтение реестра mcprig.** `~/.local/state/mcprig/registry.json` содержит токены наборов и хэши пароля панели | Полностью — если state-каталог не смонтирован |
| 4 | **Эксфильтрация наружу.** POST на `https://collector.example/…` | Полностью при `--unshare-net`; частично при allowlist-egress |
| 5 | **Запуск процессов.** `npx evil`, `curl … | sh`, `sudo` | Частично: seccomp/Landlock не запрещают `execve`, но без сети и без файлов польза падает; `no-new-privileges` убивает setuid-путь |
| 6 | **Запись/порча данных.** `rm -rf`, шифровальщик, правка `~/.zshrc`, git-хуков | Полностью при read-only корне и одном rw-каталоге |
| 7 | **Supply chain: `npx -y pkg` / `uvx pkg`.** Версия не запинена, latest подменяют | Не лечит, но ограничивает blast radius; лечится пином версии и вендорским источником |
| 8 | **Tool poisoning / prompt injection.** Вредоносная инструкция в `description` инструмента или в результате вызова | **Не лечит.** Нужны пиннинг описаний, показ diff, allowlist инструментов |
| 9 | **Confused deputy.** Сервер с нашими OAuth-правами делает то, чего пользователь не разрешал | Не лечит; частично — раздельные креды на сервер |
| 10 | **Fork-бомба / OOM.** `:(){ :|:& };:` или утечка памяти | Только через лимиты (rlimit/cgroup) — сама песочница их не даёт |
| 11 | **DNS rebinding на локальный HTTP-сервер** (спека, раздел Local MCP Server Compromise) | Применимо к HTTP-ветке: bind только loopback + токен |
| 12 | **Атака на оркестратор песочницы.** Кривой парсер конфига образа превращается в флаги рантайма (см. CVE-2026-55887) | Отрицательный эффект: песочница добавляет свой attack surface |

Спека MCP (2026-07-28) прямо называет наш класс архитектуры: «local proxy service
sits between the client and MCP servers, spawning servers as child processes via the
`stdio` transport» — и требует от таких прокси «sandboxing or containerization for
spawned processes», «restrict file system access», «log all `stdio` transport usage».

---

## 3. Что даёт и чего не даёт модель MCP

- **Транспорт stdio сам по себе — не песочница.** Он ограничивает, *кто* может
  говорить с сервером (только родитель), но процесс наследует полные права
  пользователя. Это сказано и в спеке, и в разборах.
- **Capability flags** (`tools.listChanged` и пр.) — про функциональность, не про
  безопасность; никакого «разрешения на файл» в протоколе нет.
- **OAuth (RFC 9700, 2.1)** относится к HTTP-серверам: наши клиентские потоки,
  `sameHostRedirect`, audience-проверка токена. К stdio не применяется.
- **Актуальные SEP по безопасности** (2026-07-28): confused deputy, token
  passthrough (запрещён), SSRF, state handle hijacking, session hijacking,
  local MCP server compromise. Для нас практически релевантны последний и
  «stdio через прокси = путь эскалации от XSS к RCE».
- **Реестр MCP** проверяет владение namespace, но «delegates security scanning» —
  подпись не равна безопасности кода.

---

## 4. Варианты изоляции

### 4.1 `bwrap` (bubblewrap) — wrapper, userns, без root

`bwrap --ro-bind /usr /usr … --unshare-all --die-with-parent -- cmd args`.
Плоское дерево ФС собирается из bind-монтирований, сеть/PID/IPC — отдельные
namespaces. Используется Flatpak и (по документации) sandbox-режимом Claude Code
на Linux.

- **Плюсы:** нулевые зависимости у нас (бинарь уже стоит), stdio наследуется как есть,
  `exec.Command` не меняется — только argv; старт ~10 мс; unprivileged.
- **Минусы:** нет лимитов CPU/RAM/PID (нужен `prlimit` рядом); нет egress-allowlist
  по хостам (только «сеть есть / сети нет»); требует включённого userns;
  конфигурация bind-ов хрупкая для node/python (нужны `/etc/ssl`, `/etc/resolv.conf`,
  `/usr/lib/node_modules`, `TMPDIR`).
- **Проверено на этом хосте** (2026-11-10, ядро `6.12.115+deb13.1-amd64`,
  `/proc/sys/kernel/unprivileged_userns_clone=1`): `bwrap --unshare-all` даёт
  `NET-BLOCKED`, `/home/<user>/.ssh` отсутствует, `/home` можно отдать пустым tmpfs.

### 4.2 `systemd-run --user` — свойства юнита, без root

`ProtectHome=`, `ProtectSystem=`, `PrivateNetwork=`, `PrivateTmp=`,
`RestrictAddressFamilies=`, `SystemCallFilter=`, `DynamicUser=`, `MemoryMax=`,
`TasksMax=`.

- **Ключевой факт:** в per-user инстансе эти опции доступны, но только через
  **неявный `PrivateUsers=`**, то есть требуют userns (man systemd.exec: «only
  available for system services, or for services running in per-user instances … in
  which case `PrivateUsers=` is implicitly enabled»). ArchWiki до сих пор пишет, что
  user-юниты «cannot be hardened properly» — это устаревшая формулировка, но
  направление верное: граница слабее, чем у системного юнита.
- **Проверено:** `systemd-run --user --wait --pipe -p ProtectHome=tmpfs
  -p PrivateNetwork=yes` → `~/.ssh` недоступен, в netns только `lo`, старт юнита
  ~886 мс.
- **Минусы для нас:** `--scope` **не принимает** exec-свойства (`Unknown assignment:
  ProtectHome=tmpfs` — проверено), то есть прозрачно обернуть наш `exec.Command`
  нельзя. Остаётся `--pipe` + уникальный `--unit=`, а тогда lifecycle (kill, wait,
  рестарт) уходит в `systemctl --user stop`, а не в `cmd.Process.Kill`. Плюс
  зависимость от systemd и от версии.

### 4.3 Docker / rootless Podman — контейнер

`docker run -i --rm --read-only --network none --cap-drop ALL --security-opt
no-new-privileges --user 1000:1000 --memory 512m --pids-limit 256 -v dir:/data:ro image`.

- **Плюсы:** привычно, есть готовые образы node/python, лимиты и cgroup из коробки,
  stdio через `-i` работает, легко добавить egress-политику.
- **Минусы:** **общий kernel — это не граница для недоверенного кода** (консенсус
  2026 года: контейнер = упаковка + лимиты, не изоляция). Требует образа на каждый
  сервер (сборка/тяга, supply chain образа), `--network none` ломает API-серверы,
  а allowlist по хостам надо строить отдельно.
- **На хосте:** Docker 29.9.3, **rootless** (`SecurityOptions` содержит `name=rootless`),
  пользователь не в группе `docker`, но в `sudo`. Rootless-демон — меньше риска,
  чем root-демон с `docker.sock`.
- **Урок:** CVE-2026-55887 в `docker/mcp-gateway` (0.21.0–0.42.2, фикс 0.42.2) —
  YAML из лейбла образа `io.docker.server.metadata` массово заполнял структуру с
  runtime-полями, и в `docker run` уезжали `-v /:/host`, `-u root`,
  `-v /var/run/docker.sock:…` → RCE как UID 0 на хосте. `--security-opt
  no-new-privileges` не спасает, потому что пробой происходит при создании
  контейнера. Мораль: код, который собирает argv песочницы, — тоже attack surface.

### 4.4 gVisor (`runsc`) — user-space kernel

Sentry (ядро на Go в userspace) + Gofer (доступ к ФС через 9P). OCI-совместим,
включается как runtime Docker.

- **Плюсы:** реально сильнее контейнера, syscalls не идут в ядро хоста напрямую;
  совместимость с обычными Linux-бинарями (с оговорками).
- **Минусы:** неполная совместимость syscall/`/proc` (node и python иногда ломаются),
  выше overhead на syscall-heavy нагрузке, установка `runsc` в Docker-конфиг
  (нужен доступ к конфигу демона — у нас rootless, значит конфиг свой, это плюс),
  отдельная эксплуатация.

### 4.5 microVM: Firecracker / microsandbox / E2B

Своё ядро под KVM. Firecracker — то, на чём работают Lambda/Fargate; E2B даёт
Firecracker-песочницы как сервис; microsandbox — открытый self-hosted вариант на
libkrun, заявлен rootless-запуск и boot <200 мс; PandaStack заявляет 179 мс p50 на
создание и ~49 мс на restore.

- **Плюсы:** сильнейшая граница из практичных; правильный выбор для «совсем
  недоверенных» серверов, которым нужен полноценный OS.
- **Минусы:** нужен KVM (`/dev/kvm`), образы, сеть/ФС через vsock и монтирования;
  для одного разработчика на ноутбуке — заметная инфраструктура. Оправдано, когда
  серверов десятки и они arbitrary.

### 4.6 Landlock (+ seccomp) — самозащита процесса, без root

Ядро 5.13+, ABI: 4 (6.7) — `NET_BIND_TCP`/`NET_CONNECT_TCP`, 5 (6.10) — `IOCTL_DEV`,
6 (6.12) — scope для abstract unix sockets и сигналов. На хосте 6.12, значит ABI 6.

- **Плюсы:** ноль зависимостей, применяется к себе и потомкам, идеально как
  «вторая стенка» внутри нашей же песочницы или как fallback там, где нет bwrap.
- **Минусы:** правила сетевые **по портам, не по адресам** (443 — значит любой хост);
  не ограничивает `execve`; применение требует либо helper-процесса перед `execve`,
  либо `setpriv`/`landlock-restrict`-обёртки. Из Go — через `x/sys/unix` в
  re-exec-хелпере нашего же бинаря (`mcprig __sandbox-exec`).
- Готовые обёртки того же класса: `agent-jail` (Landlock), `agent-lock` (eBPF LSM
  на `lsm/file_open`).

### 4.7 WASM-рантаймы

Для «чистых вычислений» (парсинг, преобразование) WASM даёт capability-модель и
мгновенный старт. Но зрелого рантайма, в который кладут произвольный node/python
MCP-сервер, в 2026 нет; для наших серверов это не вариант, а ниша.

### 4.8 Готовые MCP-шлюзы

Docker MCP Catalog/Gateway (300+ проверенных образов, SBOM, инъекция секретов,
изолированный контейнер на сервер), Docker Sandboxes (VM-песочница на агента,
egress-политики `sbx policy`). Как продукты решают «много серверов у команды», но
это внешний слой поверх нашего прокси, а не замена: mcprig всё равно останется
точкой, где спавнится процесс.

---

## 5. Рекомендация

Главный выбор: **bwrap как режим по умолчанию для stdio-серверов с флагом
`untrusted`, Docker — опция для тяжёлых node/python-серверов, microVM — третий этап.**
Причина: bwrap даёт 80% защиты при ~200 строках кода, не требует root, не меняет
транспорт, проверен на этом хосте и fail-closed (если userns выключен — сервер не
запускается, а не запускается «как раньше»).

### Этап 0 — без песочницы, один вечер (закрывает сценарии 1, 3, 7)

1. `Env` больше не `nil`: в `cmd/mcprig/main.go:204` и `:285` подставить
   `managerEnv`-эквивалент. Ребёнок получает whitelist + `${VAR}` своего сервера.
2. `Server.Env` становится «только явно разрешённое»; добавить `envAllow []string`.
3. Валидация: `npx -y` без версии и `uvx` без `==` — предупреждение в `_configError`
   или отдельное поле `warnings`.
4. `sandbox: "required"` для серверов с тегом `untrusted`: пока песочницы нет —
   сервер помечается `disabled` с причиной.

### Этап 1 — bwrap (закрывает 2, 4, 6, частично 5 и 10)

Конфиг: `Server.Sandbox *Sandbox` (`internal/config/config.go:24-37`), поля:

```json
"sandbox": {
  "mode": "bwrap",            // none | bwrap | docker
  "onUnavailable": "deny",    // deny | warn | allow
  "ro": ["/usr", "/lib", "/lib64", "/bin", "/etc/ssl", "/etc/resolv.conf"],
  "rw": [],                   // пусто по умолчанию; рабочий каталог сервера — отдельно
  "network": "none",          // none | allow
  "memoryMb": 2048,
  "pids": 256,
  "timeoutSec": 30
}
```

Код: пакет `internal/sandbox`, функция `Wrap(srv *config.Server, argv []string)
(`sandbox.Result, error)`; вызов в `internal/downstream/connect.go` перед
`exec.Command`. Для `mode:"bwrap"` argv становится
`bwrap --ro-bind … --unshare-all --die-with-parent --new-session -- prlimit --as=…
--nproc=… -- <cmd>`: `prlimit` стоит **внутри** bwrap, иначе лимиты применились бы
к самому bwrap, а не к потомку. `cwd` монтируется `--ro-bind` и задаётся
`--chdir`. Дедлайн вызова живёт в `mcpserver` и сбрасывает сессию после срабатывания.

Почему не `systemd-run --user`: `--scope` не принимает `ProtectHome` (проверено:
`Unknown assignment`), а `--pipe` уводит lifecycle в systemctl. bwrap прозрачнее.

### Этап 2 — Docker-режим и управляемый egress (закрывает 4 точнее, 7 по образам)

`mode:"docker"`: argv = `docker run -i --rm --read-only --cap-drop ALL
--security-opt no-new-privileges --user 1000:1000 --memory … --pids-limit …
--network <none|mcp-egress> -v <cwd>:/work:ro <image>`. Образ — с пином по digest.
Egress-allowlist: отдельная сеть + прокси-сайдкар, либо `--network none` и
unix-socket мост к разрешённому API. OAuth-серверы остаются на HTTP-транспорте и
песочницы не требуют.

### Этап 3 — microVM для «высокого риска»

`mode:"microvm"` через microsandbox (rootless, libkrun) или Firecracker, если на
хосте есть `/dev/kvm`. Только для серверов, которым нужен полный OS и которым мы не
доверяем совсем. Не раньше, чем появятся реальные такие серверы.

### Секреты (отдельно от песочницы)

Не давать env целиком; инжектить по требованию: `envAllow` на сервер, значение —
из `Secrets` при старте сессии, не в `servers.json`. Для HTTP — заголовки уже
инжектятся (`connect.go:111`), плюс `sameHostRedirect` (:157). Для stdio секрет
живёт в env ребёнка; уменьшить окно — передавать его только серверам, где он
объявлен (`envRefs`, `runtime.go:495`).

---

## 6. Проверка, что песочница держит

Каждый пункт — команда, которую гоняет `bash scripts/sandbox-e2e.sh` (или Go-тест
`internal/sandbox`), с ожидаемым исходом. Без зелёного прогона режим не считается
внедрённым.

| # | Тест | Ожидание |
| --- | --- | --- |
| 1 | `cat ~/.ssh/id_ed25519` | `No such file or directory` |
| 2 | `cat ~/.local/state/mcprig/registry.json` | нет файла |
| 3 | `curl -m 3 https://example.com` | отказ (нет маршрута/DNS) |
| 4 | `echo x > /etc/pwned` | `Read-only file system` |
| 5 | `env` | только whitelist + свои `${VAR}`, без `AWS_*`, `*_TOKEN` |
| 6 | `:(){ :|:& };:` | ограничено `--nproc`, машина жива, процесс убит |
| 7 | `python -c 'print(open("/proc/1/root/etc/shadow").read())'` | отказ (PID ns / права) |
| 8 | `mount -t tmpfs none /mnt` | `EPERM` (нет `CAP_SYS_ADMIN`) |
| 9 | `nc -U /run/user/1000/bus` | нет сокета (net ns) |
| 10 | `strace -f -e trace=openat,connect,execve` за 60 с реального вызова | отчёт: ни одного открытия вне разрешённых путей, ни одного connect вне API |
| 11 | негативный контроль: `mode:"bwrap"`, userns выключен | сервер **не стартует**, ошибка `sandbox unavailable`, а не тихий запуск |
| 12 | утечка через вывод инструмента | `redact` уже есть (`internal/mcpserver/redact_test.go`) — прогнать на новых путях |

Аудит поведения до первого доверия: `strace -f` плохо подходит, потому что PID
сервера появляется в момент, который выбирает агент; практичнее eBPF-наблюдение за
поддеревом процессов (`claudefeed --match=node --secs=60`, `exectop`) или пробный
запуск под `bwrap` + `strace` с таймаутом. Признаки, которые надо ловить: открытия
вне рабочего каталога (`~/.ssh`, `~/.aws`, `~/.config`), connect не на заявленный
API, `execve` неожиданных бинарей, чтение `/proc/*/environ`.

---

## 7. Что осталось открытым

- **Tool poisoning не решается изоляцией.** Нужен отдельный механизм: хэш
  `tools/list` (описание + схема) при первом доверии, показ diff при изменении,
  allowlist инструментов на сервер. Сейчас в коде кэш инструментов есть, пиннинга нет.
- **Egress по хостам без root.** Landlock даёт только порты, nftables — root,
  bwrap — «всё или ничего». Нужен выбор: unix-socket прокси (дорого) или
  `--network` + честное логирование (дешево, слабее).
- **Совместимость bwrap с node/python.** Точный набор bind-ов для `npx`/`uvx`
  (кэши npm/uv, `NODE_EXTRA_CA_CERTS`, `TMPDIR`) надо собрать на реальных серверах;
  риск, что «работает не всё» выше, чем у Docker.
- **HTTP-серверы.** Песочница к ним не применима; их риски — SSRF, DNS rebinding,
  унос заголовков. Часть закрыта (`sameHostRedirect`), bind только loopback и
  требование токена — не проверено.
- **Секреты в `servers.json`.** Панель пишет файл `0600`, но значения лежат в
  открытом виде; переход на инъекцию из `Secrets` в момент старта сессии — отдельная
  работа.
- **Оркестратор песочницы как attack surface.** CVE-2026-55887 показывает: парсер
  конфига образа может отдать `-v /:/host`. Значит, поля `sandbox.*` должны
  валидироваться allowlist-ом, а не склеиваться в argv как есть.
- **Ресурсные лимиты.** `prlimit` покрывает `nproc`/`as`, но не CPU и не суммарную
  память поддерева; cgroup-лимиты без systemd-юнита недоступны.
- **Кто владелец решения.** Песочница меняет доверие к серверам; приёмку должен
  давать человек, а не агент.

---

## Источники

Проверено 2026-11-10.

1. MCP, Security Best Practices (спека 2026-07-28) — local server compromise,
   sandboxing, stdio-через-прокси:
   https://modelcontextprotocol.io/docs/2026-07-28/tutorials/security/security_best_practices
2. MCP, Security Best Practices (та же страница, базовая форма):
   https://modelcontextprotocol.io/specification/2026-07-28/basic/security_best_practices
3. Sandboxing Local MCP Servers, eseospace, 2026-10-28:
   https://eseospace.com/blog/mcp-sandboxing-local-servers/
4. MCP Supply Chain Risks (postmark-mcp 1.0.16, CVE-2025-6514, реестр), eseospace,
   2026-10-28: https://eseospace.com/blog/mcp-supply-chain-risks/
5. Best Sandboxes for Running MCP Servers (2026), PandaStack, 2026-07-19:
   https://www.pandastack.ai/blog/best-sandbox-for-mcp-servers-2026/
6. Docker MCP Catalog и Gateway: https://docs.docker.com/ai/mcp-catalog-and-toolkit/catalog/ ,
   https://docs.docker.com/ai/mcp-catalog-and-toolkit/mcp-gateway/
7. Docker Sandboxes (VM-песочница, egress-политики): https://docs.docker.com/ai/sandboxes/
8. CVE-2026-55887, docker/mcp-gateway (фикс 0.42.2):
   https://www.kodemsecurity.com/cve-archive/cve-2026-55887
9. gVisor, архитектура Sentry/Gofer: https://gvisor.dev/docs/
10. Landlock LSM, unprivileged sandboxing (ABI 1–6), systemshardening, 2026-04-27:
    https://www.systemshardening.com/articles/linux/landlock-lsm/
11. Landlock, ядерная документация: https://docs.kernel.org/userspace-api/landlock.html
12. systemd.exec (ProtectHome/ProtectSystem в per-user инстансе через неявный
    PrivateUsers): https://www.freedesktop.org/software/systemd/man/systemd.exec.html
13. systemd/Sandboxing, ArchWiki: https://wiki.archlinux.org/title/Systemd/Sandboxing
14. bubblewrap (userns, unprivileged): https://github.com/containers/bubblewrap
15. How to Audit a Local MCP Server on Linux (claudefeed, exectop, agent-lock,
    agent-jail), 2026: https://yeet.cx/topical-takes/audit-local-mcp-server-linux
16. Microsandbox (rootless microVM, <200 мс): https://microsandbox.dev/ ,
    https://github.com/superradcompany/microsandbox
17. E2B (Firecracker microVM): https://e2b.dev/resources/best-code-execution-sandboxes-for-ai-agents
18. Tool Poisoning Attacks, Invariant Labs, 2025-04-01:
    https://invariantlabs.ai/blog/mcp-security-notification-tool-poisoning-attacks
19. MCP-Scan, Invariant Labs, 2025-04-11: https://invariantlabs.ai/blog/introducing-mcp-scan
20. Замеры хоста (2026-11-10): ядро `6.12.115+deb13.1-amd64`,
    `/proc/sys/kernel/unprivileged_userns_clone=1`, Docker `29.9.3` rootless,
    bwrap `--unshare-all` → `NET-BLOCKED`, `/home` отсутствует;
    `systemd-run --user --wait --pipe -p ProtectHome=tmpfs -p PrivateNetwork=yes`
    → только `lo`, ~886 мс; `systemd-run --user --scope -p ProtectHome=tmpfs`
    → `Unknown assignment: ProtectHome=tmpfs`.
