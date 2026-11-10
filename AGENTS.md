# mcp-manager (Go)

Один бинарник: MCP-прокси на «набор», панель управления, дашборд.

## Правила репозитория

- **Контракт важнее кода.** Поведение берётся из `docs/reference/ts-manager-contract.md`, а не из TS-исходников. Ни одна строка TS сюда не переносится: перенос строки тянет баг и убивает смысл переписывания.
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
bash scripts/dev-real-set.sh      # стенд на живых серверах: см. docs/dev-real-servers.md
```

Go-тулчейн: локально 1.24.4, модуль требует 1.27.3 — `GOTOOLCHAIN=auto` скачивает нужный тулчейн сам (проверено: `GOTOOLCHAIN=go1.27.3 go version`).

## Ссылки

- Стенд на настоящих серверах: `docs/dev-real-servers.md` (serena, socraticode, xerj).
- Эталон: `/home/sintez/git/lazy-mcp@5a963c5` (заморожен) и распакованный менеджер `/tmp/opencode/mm/mcp-manager`.
- MCP Go SDK: `github.com/modelcontextprotocol/go-sdk` v1.8.0, протокол по умолчанию `2026-07-28`.
