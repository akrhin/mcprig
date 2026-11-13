[← Docs index](../index.md)
[EN](../../en/dev/release.md) · RU

# Выпуск релиза

Что происходит по порядку, когда пушнули тег `vX.Y.Z`, и где грабли. Что лежит
в архивах и как они ставятся — в [install.md](../install.md); здесь этих чисел
нет.

## Конвейер

1. Тег `vX.Y.Z` на `master`, пуш.
2. Gitea CI (`.gitea/workflows/release.yml`) гоняет проверки — `go vet`, тесты
   с `-race`, `govulncheck`, `gofmt` — затем `goreleaser`: четыре платформы,
   `checksums.txt`, **черновик** релиза в Gitea и образы `slim`/`full` в реестр.
   Job'ы сериализованы (`concurrency: mcprig-release`): второй тег ждёт, а не
   отменяет первый.
3. Черновик публикует человек; CI этого не делает.
4. Воркфлоу GitHub (`.github/workflows/release.yml`) идёт по тому же тегу:
   пересобирает те же архивы с `goreleaser --skip=publish`, собирает и пушит
   образы `slim`/`full` в `ghcr.io/akrhin/mcprig`, заливает архивы через `gh` —
   так `.goreleaser.yaml` остаётся один, с одними `gitea_urls`/`draft`.
   Тестов и `govulncheck` там нет.
5. Заметки релиза — раздел `CHANGELOG.md` для тега, вырезает
   `scripts/release-notes.sh`.

Релиз можно выпустить и руками: `gh workflow run release.yml -f tag=vX.Y.Z`.

## Перед тегом

```bash
gofmt -l .                              # пусто
go build ./... && go vet ./...
go test ./... -race -count=1 -p 1       # -p 1: manager и e2e делят порты 9100-9199
scripts/sandbox-e2e.sh                  # 14/14
goreleaser check
goreleaser release --snapshot --clean   # собрать архивы, ничего не публикуя
```

`goreleaser` на хосте разработки не установлен — настоящую сборку делает CI.

## Строка версии

Версию несёт `-X main.version`; обычный `go build` печатает `dev`. Для образов
передаётся `--build-arg VERSION=0.4.0` — **без** ведущей `v`: значение, которое
начинается с `v`, отвергается и сборка падает.

## Поставить релиз на хост

```bash
tar -xzf mcprig_<version>_linux_amd64.tar.gz
grep 'linux_amd64' checksums.txt | sha256sum -c -
install -m 755 mcprig ~/.local/bin/.mcprig.new
mv -f ~/.local/bin/.mcprig.new ~/.local/bin/mcprig
systemctl --user restart mcprig.service
```

Копирование поверх работающего бинарника даёт `ETXTBSY`; установка под другим
именем с последующим `mv` — нет. Дальше `mcprig doctor` должен закончиться
строкой `итог: 0 проблем`, а каждый набор — ответить своей версией.

## См. также

- [development.md](development.md) — сборка, тесты, живой стенд.
- [install.md](../install.md) — состав архивов, юниты, образы, каталог состояния.
- [troubleshooting.md](../troubleshooting.md) — набор, который не поднялся после перезапуска.
