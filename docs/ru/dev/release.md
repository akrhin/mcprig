[← Docs index](../index.md)
[EN](../../en/dev/release.md) · RU

# Выпуск релиза

Что происходит по порядку, когда отправлен тег `vX.Y.Z`, и где ловушки. Что лежит
в архивах и как они ставятся — в [install.md](../install.md); здесь этих чисел
нет.

## Конвейер

Два репозитория, один сборщик. GitHub — источник истины для проверок и релизов;
Gitea держит зеркало кода и запускает ровно один воркфлоу, `images`, потому что
реестр хоста с GitHub недоступен.

1. Тег `vX.Y.Z` на `master`, пуш сначала в GitHub, потом в Gitea.
2. GitHub `ci` идёт на каждый пуш и pull request: `gofmt`, `go mod tidy -diff`,
   `go vet`, `go build`, кросс-сборка под `darwin/arm64`, `darwin/amd64` и
   `linux/arm64`, `go test ./... -race -count=1 -p 1` с покрытием в артефакте,
   `golangci-lint` (`.golangci.yml`: стандартные линтеры плюс `gosec`,
   `bodyclose`, `errorlint`, `gocritic`, `noctx`, `unparam`), `govulncheck`,
   `gitleaks` по всей истории, `scripts/docs-check.sh`, `node --check web/app.js`.
   `docker build` обеих целей образа идёт только на pull request и только когда
   изменились `Dockerfile`, `.dockerignore`, `go.mod`/`go.sum` или `docker/` —
   по тегу образы собираются всё равно, и пуш в `master` эту работу не повторяет.
3. GitHub `codeql` сканирует Go и JavaScript панели на каждый пуш и раз в неделю;
   `.github/codeql/codeql-config.yml` держит вне сканирования сгенерированные
   страницы (`docs/diagrams/`) и макеты интерфейса (`design/ui/`) — это вывод,
   а не код; `dependency-review` валит pull request, который добавляет зависимость с
   уязвимостью высокой важности.
4. GitHub `release` идёт по тегу. `ci` на теги не висит, поэтому проверки
   повторяет сам релизный воркфлоу — `go vet ./...` и
   `go test ./... -race -count=1 -p 1` — и только потом публикует. Затем
   `goreleaser release --clean --skip=publish` собирает четыре платформы и
   `checksums.txt` и сам ничего не публикует, `gh release` создаёт страницу (или
   догружает в неё файлы) и принудительно ставит `--draft=false --latest`, а
   `docker build`/`push` отправляют `<version>-slim`, `<version>-full`, `latest`
   и `latest-full` в `ghcr.io/akrhin/mcprig`. Руками ничего не публикуется.
5. Gitea `images` идёт по тому же тегу — и только по тегу, ручного запуска у него
   нет — и делает одно: собирает `slim` и `full` и пушит их в реестр хоста из
   `secrets.REGISTRY`. Раннер GitHub туда не может — `docker login` к этому
   реестру отвечает `403` — поэтому образы для хоста приходят из Gitea, и релиз их
   не ждёт. Без секретов реестра или без docker-сокета в job-контейнере раннера
   воркфлоу пишет об этом и выходит с `0`: зелёный прогон `images` сам по себе не
   доказывает, что образы отправлены.
6. Примечания к релизу — раздел `CHANGELOG.md` тега, его вырезает
   `scripts/release-notes.sh`.

Что держит в безопасности сам конвейер: каждый экшен прибит к SHA коммита с
версией в комментарии, `permissions` — `contents: read`, если джобе не нужно
больше, `persist-credentials: false` не оставляет токен checkout в `.git/config`,
pull request никогда не получает секреты репозитория, а сканеры ставят свои
инструменты (`gitleaks`, `govulncheck`), а не сторонние экшены. Публичные
репозитории на стандартных раннерах не тарифицируются, поэтому проверки не
урезаны ради экономии.

Красный прогон сам по себе ничего не блокирует: ни один из форжей не включает
обязательные проверки по умолчанию — прогон читают глазами. Релиз можно выпустить
и руками: `gh workflow run release.yml -f tag=vX.Y.Z`.

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
передаётся `--build-arg VERSION=0.6.3` — **без** ведущей `v`: значение, которое
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
