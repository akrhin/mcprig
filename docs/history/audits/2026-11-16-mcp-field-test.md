# Полевой тест MCP за mcprig — 2026-11-16

Этап 1 задачи владельца: протестировать **каждый** сервер набора `main` по-настоящему,
записать проблемы, понять, что и зачем нужно. Репозиторий: `mcprig` 0.6.3, набор `main`
(`http://192.168.1.92:9101/mcp`), 18 серверов, все были `healthy`.

**17.11:** по итогам теста `scalar`, `mermaid-chart` и `snyk` сняты с набора — в наборе
15 серверов; разделы ниже оставлены как история наблюдений.

## 1. Повод

Статистика набора (`GET :9101/stats`, окно 183 вызова):

| сервер | вызовов | заметка |
| --- | --- | --- |
| arcane-mcp | 135 | из них 95 — `environment_vulnerabilities_list` |
| mcprig (мета) | 28 | `list_servers`, `list_commands` |
| serena | 15 | `initial_instructions` 7 раз — каждый раз заново |
| portainer-mcp | 3 | `docker_raw` 2/2 с ошибкой |
| socraticode | 2 | `codebase_search` 1/1 с ошибкой |
| **остальные 13** | **0** | context7, xerj, open-design, tavily, playwright, raven, designer-skill, gitea, dap-mcp, deepwiki, mermaid-chart, scalar, snyk, code-review-graph |

То есть набор держит 18 серверов, а пользуются четырьмя с половиной — и выбирают не
по задаче, а по привычке. Причина не только в привычке: у прокси нет ни одного механизма,
который подсказал бы нужный сервер, а формы ответов у серверов несовместимы (§5).

## 2. Метод

Каждый сервер: `list_commands` → `describe_commands` (2–4 тула) → **реальные вызовы**
(2–11 на сервер) с настоящими аргументами → независимая сверка результата (`ls`, `curl`,
`git`, повторный вызов другим инструментом) → минимум один отказ (неверный параметр,
несуществующий объект, отсутствующий токен). Опасные операции (delete/exec/deploy)
не вызывались — помечены «не проверено». Досье: `.tmp/sessions/mcp-audit-20261116/mcp/*.md`
(18 файлов, ~680 строк).

## 3. Вердикт по серверам

| сервер | тулов | что реально даёт | вердикт |
| --- | --- | --- | --- |
| serena | 29 | LSP: `find_symbol` 0.4 с, `find_referencing_symbols` с фрагментами кода, `get_symbols_overview`; единственная семантика типов (поле `lsp` в opencode мёртвое) | **ядро**, но один активный проект на процесс |
| xerj | 22 | поиск пассажа по индексу: 56 мс, точный; PDF/логи/CSV/markdown | **ядро**, но индексов не было ни одного |
| socraticode | — | смысловой поиск по коду, 7 с | держать, индекс сломан |
| code-review-graph | 31 (4 наружу) | blast radius файла, risk-ranked diff; 84 с на сборку графа | держать узко |
| gitea | 55 | issues/PR/Actions/логи CI | держать, 2 ловушки |
| portainer-mcp | 87 | Docker на удалённых хостах, K8s/Helm/Edge, `docker_raw` | сузить toolsets |
| arcane-mcp | 54 | тот же парк **дешевле в 4–5 раз** + CVE + хост `hermes`, которого у Portainer нет | держать, чинить `projects_list` |
| playwright | 25 | страница, консоль, сеть, скриншот | держать |
| raven | 115 | `audit_contrast` (точные ratio), `audit_tap_targets` | держать узко |
| designer-skill | 18 | `dispatch_intent`, `check_contrast` 39 мс | держать узко, 3 гейта мертвы без roots |
| open-design | 22 | каталог артефактов и превью | держать, если им рисуют |
| dap-mcp | 13 | отладка Go/Python/Node/C по DAP; Go на дефолте **не работает** | держать после починки |
| deepwiki | 3 | «как устроен чужой репозиторий», 9–10 с | держать |
| context7 | 2 | доки библиотек по версии | держать с правилом «не пинить версию» |
| tavily-mcp | 5 | `research` (обзор с цитатами) | только за `research`; `search` дублирует `websearch` |
| **snyk** | 14 | ноль рабочих вызовов: `User not authenticated`, а `snyk_auth` запрещён при заданном `SNYK_TOKEN` | **убрать или починить env** |
| **mermaid-chart** | 1 | `tools/call` не возвращается никогда: рендер в UI-виджет ChatGPT-коннектора | **убрать** |
| **scalar** | 1 | поиск по **своим** докам Scalar (`/learn`), наша спека ему не передаётся | **убрать**, замена — `webfetch` |

## 4. Проблемы серверов (что чинить или о чём знать)

**Отдают неверный или пустой результат молча**

1. **socraticode — устаревший индекс.** `codebase_status`: `INDEX IS INCOMPLETE`,
   `Chunks stored: 2722 (partial)`, `File watcher: inactive`, граф построен 6 дней назад.
   В выдаче процитирован `"github.com/sintez/mcprig/internal/toolout"`, а в файле сейчас
   `github.com/akrhin/mcprig` (`internal/mcpserver/toolout.go:19`, переименование — коммит
   `bb83df7`). Предупреждение `⚠ … Results may be stale` есть, но в чат не попадает.
2. **xerj — ноль индексов.** `_cat/indices` пуст, дефолт `ax-*` не создан; любой поиск
   отдаёт `hits.total: 0` **без ошибки**. После `xerj autoindex docs --prefix mcprig-docs`
   (62 файла → 3051 запись, 66 с) поиск заработал: 202 совпадения за 56 мс.
3. **code-review-graph — test-gap врёт.** При `base:HEAD~8` выдал `Untested: runDoctor,
   portOpen`, хотя `callers_of runDoctor` в том же графе видит
   `cmd/mcprig/doctor_test.go:310,317`, а `portOpen` — `e2e/lifecycle_test.go:120`.
   Классификация считается по `direct`-рёбрам, а в этом репозитории `direct:0, unresolved:29`.
4. **arcane-mcp `projects_list` сломан:** `data:[]`, `grandTotalItems:0` при 23 живых
   контейнерах с compose-метками; `project_compose_list` без `projectId` →
   `missing path parameter: projectId`.
5. **gitea `list_issues` по умолчанию отдаёт PR.** 14 объектов, все — `…/pulls/N`
   (проверено мной на `limit:5`). Нужен `type:"issues"`.
6. **gitea `actions_run_read(method:"list_jobs", run_id:1770)`** молча вернул джобы
   прогона 1630 — нужен `list_jobs` через отдельный метод.
7. **raven: два тула расходятся.** `audit_page` → 78/C, `score_page` на той же странице
   → 68/D (`a11y: 0/10`, `contrast.fail_count: 6`); контраст подмешан только во второй,
   хотя `audit_page` обещает его проверять.

**Требуют неверного или неочевидного параметра**

8. **portainer-mcp:** окружение выбирается двухступенчато `environment_list` → int `Id`,
   дальше `environment` обязателен в каждом docker-туле. `docker_raw` без него →
   `Error: missing path parameter: environment`; `environment:0` → 404; строка `"16"`
   приводится и работает. Отсюда «2/2 fail» в статистике — не поломка сервера.
9. **context7:** `libraryId` с пином версии (`/modelcontextprotocol/go-sdk/v1.8.0`) →
   `Error: таймаут вызова 120000 мс` (2/2); без пина — 1.5 с и по делу. `libraryName`
   `"go-sdk"` → чужая `open-feature/go-sdk`; нужен `org/project`.
10. **scalar:** параметр `question`, в описании — «user's query»; `query` →
    `Invalid arguments … question: expected string`.
11. **xerj:** `xerj_hub_find` требует `question`, `xerj_search` — `query`; индексация
    только CLI (`xerj autoindex`), MCP-тула для неё нет; `xerj hub sync` без сети не работает.
12. **tavily:** `tavily_extract` по битому URL вернул «Detailed Results:» — пустой успех
    без `isError`; в выводе `Content: undefined`, полезное в `Raw Content:`; `topic` —
    перечисление из одного значения; `max_results` минимум 5.

**Тяжёлые по контексту или времени**

13. **deepwiki `read_wiki_contents`** — вся вика одной строкой (~33 КБ), параметра «одна
    страница» нет.
14. **portainer `container_list(all:true)`** — 120 864 байта; **arcane `containers_list`** —
    55 653 байта, `environment_vulnerabilities_list` — 33 364.
15. **CRG `detail_level:"standard"`** — 38 КБ на один файл; **open-design `list_skills`** —
    31 КБ; `get_active_context` 12.1 с, `list_agents` 18.3 с.
16. **raven `audit_contrast`** — 37.8 с; **code-review-graph build** — 84 с.

**Мёртвое в текущей конфигурации**

17. **mermaid-chart:** эндпоинт `https://chatgpt.mermaid.ai/anthropic/mcp` рендерит в
    UI-виджет, `tools/call` не возвращается: 120 с таймаут → `подключение не уложилось
    в 30s` → карантин каталога. Сервер остался `healthy:false, error:"context deadline
    exceeded"` после теста.
18. **designer-skill:** `detect_antipatterns`, `review_and_gate`, `verify_rendered` →
    `{"code":"SCOPE_VIOLATION","message":"The client lists no usable MCP root; no project
    directory is authorized.","details":{"unusableRoots":[]}}` на любой путь — у mcprig
    корней нет вовсе (§5.1).
19. **scalar:** 1 тул, ищет по докам вендора; ни файла спеки, ни URL, ни линта.

**Ломается на настройке по умолчанию**

20. **dap-mcp: Go не отлаживается вообще.** `internal/adapters/delve.go:82-86` жёстко
    ставит `mode:"debug"` → dlv 1.24.2 пересобирает цель хостовым go 1.27.4 и падает:
    `Version of Delve is too old for Go version go1.27.4 (maximum supported version 1.24…)`.
    Агент видит только `waiting for initialized event timed out after 10 seconds` —
    ни причины, ни имени тула. Обход подтверждён: `dlv --check-go-version=false`
    (свой инстанс dap-mcp) → launch 1.1 с, `stopReason:breakpoint`, `verified:1`,
    `debug_snapshot` со стеком `main.accumulate:8 → main.main:14`, `debug_evaluate`
    `["i","sum","limit","i*2"]` → `0,0,5,0`, три `debug_continue` → `1/0 → 2/2 → 3/6`.
    Python работает (launch 12 с, брейкпоинт verified).
21. **dap-mcp: мелкие ловушки, каждая стоит агента.** `threadId` обязателен, но
    Python-launch его не отдаёт (`argument "threadId" is not a float64`); `debug_step`
    не ждёт стопа, поэтому следующий `debug_evaluate` падает `could not find symbol
    value for i`; `status` в `debug_snapshot` **всегда** `running`, даже на брейкпоинте;
    `run_to_line` на `return sum` отдаёт `locals: []`; несуществующий бинарник → тот же
    10-секундный таймаут без диагностики.
22. **snyk мёртв по двум причинам, обе не в токене.** Токен валиден
    (`npx snyk@latest whoami` → `akrhin`, 6.7 с). Первая: mcprig не пропускает прокси
    в окружение ребёнка (`internal/manager/runtime.go:784`) → `User not authenticated
    (… run 'snyk_auth' first)`, а `snyk_auth` отказывает
    `Auth tool can't be called if SNYK_TOKEN env var is set` — замкнутый круг.
    С `HTTPS_PROXY` вручную аутентификация проходит и упирается во вторую причину:
    `ERROR Snyk Code is not enabled (SNYK-CODE-0005) … 403`, org
    `adb0c6d7-6a44-4930-92c9-c92d3a092be1`. Плюс `snyk_trust` на headless невозможен
    (`failed to open browser: exit status 3`), а без trust `snyk_sca_scan` →
    `folder '…' is not trusted`.
23. **`npx -y snyk@latest` не запинен** — дрейф версии и 3–7 с холодного старта на
    каждый вызов; пакет надо пинить.

## 5. Проблемы mcprig как прокси (проверено по коду и на проводе)

1. **MCP `roots` не реализованы.** `grep -rn 'roots\|Roots' --include='*.go' .` — пусто;
   в SDK `v1.8.0` это готово: `Client.AddRoots` (`client.go:666`), `ClientCapabilities.RootsV2`
   (`client.go:266`). Пока их нет, любой сервер, которому нужен каталог проекта, отвечает
   `SCOPE_VIOLATION` (§4.18).
2. **Обрезка ломает JSON.** `truncateResult` (`internal/mcpserver/toolout.go:48-84`) режет
   payload по 32768 байт (`defaultMaxBytes`, `:33`) и приписывает
   `[обрезано: N из M байт, полный ответ — /tool-out/<name>]`. Проверено на
   `deepwiki read_wiki_contents`: 32 943 симв. = 32 768 байт + маркер, обрезка **работает**,
   но получившийся текст — невалидный JSON. `container_list` у portainer и
   `containers_list` у arcane приходят в том же виде: агент видит `Invalid escape`
   и не знает, что деть.
3. **Полный ответ доступен только через curl.** Дамп пишется в `${MCPRIG_DATA_DIR}/tool-out/`
   и отдаётся по `/tool-out/<name>`; защита корректна (без токена 401, случайное имя 404,
   `..%2f..%2fregistry.json` 404), дамп помнит отпечаток ключа (`toolout.go:47`). Но
   MCP-тула «прочитать дамп» нет: без `curl` + токена из `~/.config/mcprig/main.token`
   escape hatch мёртв.
4. **Форма ответа не унифицирована — четыре вида на 18 серверов:** голая строка (deepwiki),
   `{content:[{type:"text"}]}` (serena, socraticode), сырой ES-объект (xerj),
   двойное кодирование строка-в-строке (scalar). Плюс `_meta.serverInfo` приходит только
   у части серверов. Общий парсер невозможен — отсюда ощущение «MCP не умею».
5. **Один таймаут на весь набор:** `requestTimeout 120000` (`internal/config/config.go:786`),
   per-call `timeoutMs` нет. Зависший сервер стоит агенту 2 минуты за попытку, а текст
   `Error: таймаут вызова 120000 мс` не называет ни сервер, ни команду.
6. **Health-проба проверяет `tools/list`, не `tools/call`.** mermaid-chart числился
   `healthy:true`, пока не висел на реальном вызове.
7. **Пустой успех проходит как успех.** `tavily_extract` с битым URL — «Detailed Results:»
   без `isError`.
8. **Ошибки серверов пробрасываются исключением**, без имени сервера
   (`Error: no project matches "…"`, `Error: A question must be provided.`); свои ошибки
   прокси пишет по-русски и по делу (`неизвестный сервер "nosuchserver"`,
   `calls: допустимо не больше 8 вызовов`). Языки смешаны.
9. **4 из 9 мета-тулов бесполезны:** `list_resources` и `list_prompts` возвращают `{}` для
   всех 18 серверов — ни ресурсов, ни промптов в наборе нет.
10. **`list_servers` не показывает, что урезано:** у code-review-graph наружу 4 тула из 31,
    и узнать это можно только по `list_commands`; URL, env, toolsets и лимиты агенту
    не видны. Описания в реестре расходятся с фактом: arcane «399 tools in 13 toolsets»
    при 54 в каталоге, portainer «read-only mode» при `read_only: false`.
11. **Нет подсказки по задаче:** ни поиска по тулам, ни рекомендаций «для этой задачи —
    serena». Отсюда перекос статистики.
12. **Whitelist окружения не пропускает прокси.** `internal/manager/runtime.go:784`
    передаёт ребёнку только разрешённые переменные, и `HTTP_PROXY`/`HTTPS_PROXY`/
    `NODE_USE_ENV_PROXY` в список не входят. На хосте, где выход только через прокси
    (а это наш случай: `192.168.1.83:1080`, переменные ставит billion-context), любой
    stdio-сервер, ходящий в сеть, получает «нет сети» и врёт про аутентификацию —
    именно так snyk отвечал `User not authenticated` при валидном токене. Резолвер
    `"HTTPS_PROXY":"${HTTPS_PROXY}"` в записи сервера уже умеет, но по умолчанию
    не делает.

## 6. Что делать в первую очередь

| | действие | цена |
| --- | --- | --- |
| 1 | Реализовать `roots` в mcprig (каталог проекта из конфига набора) | небольшая фича, SDK готов |
| 2 | Обрезку делать по структуре, а не по байтам: резать элементы массива/поля объекта, JSON оставлять валидным | правка `toolout.go` |
| 3 | MCP-тул `get_tool_out(name)` вместо curl | малая |
| 4 | `codebase_index` для socraticode, `xerj autoindex` для нужных каталогов (по расписанию) | без кода, эксплуатация |
| 5 | Убрать из набора `mermaid-chart` и `scalar` | панель |
| 6 | Записать в набор «как звать»: правила по серверам (§3) в описания серверов | текст |
| 7 | `per-call timeoutMs` и текст таймаута с именем сервера/команды | малая |
| 8 | dap-mcp: обновить dlv до 1.27.x или передавать `--check-go-version=false` (правка адаптера `internal/adapters/delve.go:82-86` в самом dap-mcp) | малая |
| 9 | snyk: `"HTTPS_PROXY":"${HTTPS_PROXY}"` в записи сервера, пин `snyk-mcp` версии, включить Snyk Code в org или убрать сервер | конфиг + панель |
| 10 | Таймаут `debug_launch`/`launch` возвращать с текстом сервера и тула, а не «waiting for initialized event timed out» | малая |

## 7. Рецепты: задача → сервер

| задача | первый вызов |
| --- | --- |
| где объявлено / кто вызывает | serena `find_symbol` / `find_referencing_symbols` |
| как устроено по смыслу | socraticode `codebase_search` (после `codebase_index`) |
| пассаж в PDF/логе/CSV/архиве сессий | xerj `xerj_search` с `prefix` или `index` |
| что заденет эта ветка | code-review-graph `detect_changes`/blast radius (после `build`) |
| чужая библиотека: API и примеры | context7 `resolve-library-id` → `query-docs`, без пина версии |
| как устроен чужой репозиторий | deepwiki `ask_wiki_question` |
| свежий факт из веба | `websearch`; обзор с цитатами — tavily `tavily_research` |
| issues/PR/логи CI | gitea `list_issues(type:"issues")`, `list_pull_requests` |
| «что с парком контейнеров» | arcane `container_status_count`, `environment_vulnerability_summary` |
| Docker-API напрямую, K8s, стеки | portainer `docker_raw` / toolsets |
| страница: консоль, сеть, скриншот | playwright |
| контраст и тап-таргеты | raven `audit_contrast`, `audit_tap_targets` |
| артефакт дизайна и превью | open-design `get_project` |
| отладка с точкой останова | dap-mcp `debug_launch` c `breakpoints` → `debug_snapshot` |
