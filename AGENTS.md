# mcprig (Go)

Один бинарник: MCP-прокси на «набор», панель управления, дашборд.

## Правила репозитория

- **Контракт важнее кода.** Поведение берётся из `docs/history/reference/ts-manager-contract.md`, а не из TS-исходников. Ни одна строка TS сюда не переносится: перенос строки тянет баг и убивает смысл переписывания.
- **Дефекты эталона — требования, а не наследство.** Список обязательных исправлений — в конце контракта (auth fail-closed, allowlist env, токены не уходят в клиент).
- Ворота этапа — одна команда, названная в `docs/CURRENT-GOAL.md`. Агент гоняет её сам.
- Один коммит — одно осмысленное изменение; `git commit` без `--no-verify` (хуки проверяют сборку и тесты).
- Документация — часть изменения: поведение, команды, конфиг или вывод изменились → `docs/en/` и `docs/ru/` вместе и строка в `CHANGELOG.md` под `## [Unreleased]`; обновлять нечего — сказать об этом в итоге.
- Оформление issue и PR — `CONTRIBUTING.md` / `CONTRIBUTING.ru.md`; шаблоны — `.gitea/ISSUE_TEMPLATE/` и `.gitea/PULL_REQUEST_TEMPLATE.md`.

## Команды

```bash
go build ./...                    # сборка
go vet ./...                      # статика
go test ./... -race -count=1 -p 1  # тесты; -p 1 обязателен: пакеты manager и e2e
                                  # поднимают наборы в общем диапазоне 9100–9199
go test ./e2e -run TestLifecycle -v   # панель + набор + stdio-даунстрим, без подготовки
bash scripts/contract-e2e.sh      # контракт против BASE (по умолчанию 127.0.0.1:9091)
bash scripts/sandbox-e2e.sh       # bwrap-песочница на живом хосте: 14/14 PASS
bash scripts/docs-check.sh        # ворота документации: ссылки, личные пути, сироты, паритет языков, docs/anchors.txt
bash scripts/dev-real-set.sh      # стенд на живых серверах: см. docs/dev-real-servers.md
```

Пакет, чьи тесты поднимают stdio-серверы, обязан иметь `TestMain` с
`downstream.RunChildGuardMode(os.Args)`: сторож — это `os.Executable()`, и без
перехвата тестовый бинарник вместо сервера запускает тесты (песочница при этом
молча «протекает»).

Go-тулчейн: модуль требует 1.27.4, и столько же стоит на хосте разработки; `GOTOOLCHAIN=auto` подтянет нужный тулчейн, если локальный отстал (проверено: `GOTOOLCHAIN=go1.27.4 go version`).

## Диаграммы и картинки

`docs/diagrams/` — по файлу `.json` (спецификация) → `.html` (archify) → `.png` (растр)
на каждом языке. Правится только `.json`, остальное перегенерируется:

```bash
archify finalize architecture docs/diagrams/mcprig.why.en.json docs/diagrams/mcprig.why.en.html \
  --quality showcase --out-dir .tmp/diagrams --json
```

`finalize` прогоняет validate → deliver → check → browser-check; второй прогон в тот же
каталог падает на `viewer/evidence-path-conflict` — каждому прогону свой `--out-dir`, а
`*.delivery.json` и `.review/` в репозиторий не коммитятся. PNG снимается headless-Chrome
с `--force-device-scale-factor=2`, из копии `.html` с добавленным в `<head>` стилем
`.toolbar,.diagram-nav{display:none!important}` (иначе в кадр попадают кнопки вьюера), в
тёмной теме: `"…?theme=dark"`. Карточки-подсказки в режиме `?embed=1` не рисуются —
для PNG этот параметр не ставить.

Скриншот панели для README (`design/ui/panel.png`) — живая панель под
`systemd-run --user` на отдельном порту, тёмная тема, EN, 1280 CSS px при DPR 2;
прежний кадр уезжает в `design/ui/archive/panel-<версия>.png`.

## Ссылки

- Стенд на настоящих серверах: `docs/dev-real-servers.md` (serena, socraticode, xerj).
- Эталон: замороженный `lazy-mcp@5a963c5` (upstream-совместимый пакет); поведение, которое переносим, — `docs/history/reference/ts-manager-contract.md`.
- MCP Go SDK: `github.com/modelcontextprotocol/go-sdk` v1.8.0, протокол по умолчанию `2026-07-28`.
