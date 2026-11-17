[← Docs index](../index.md)
EN · [RU](../../ru/dev/release.md)

# Release

What happens when a `vX.Y.Z` tag is pushed, in order, plus the traps. What the
archives contain and how they are installed is in [install.md](../install.md);
this page does not repeat those numbers.

## Pipeline

Two repositories, one builder. GitHub is the source of truth for checks and
releases; Gitea keeps a mirror of the code and runs exactly one workflow,
`images`, because the host registry cannot be reached from GitHub.

1. Tag `vX.Y.Z` on `master`, push to GitHub first, then to Gitea.
2. GitHub `ci` runs on every push and pull request: `gofmt`, `go mod tidy -diff`,
   `go vet`, `go build`, cross-build for `darwin/arm64`, `darwin/amd64` and
   `linux/arm64`, `go test ./... -race -count=1 -p 1` with coverage uploaded as
   an artifact, `golangci-lint` (`.golangci.yml`: standard linters plus `gosec`,
   `bodyclose`, `errorlint`, `gocritic`, `noctx`, `unparam`), `govulncheck`,
   `gitleaks` over the whole history, `scripts/docs-check.sh`, `node --check
   web/app.js`. `docker build` of both image targets runs on a pull request
   only, and only when `Dockerfile`, `.dockerignore`, `go.mod`/`go.sum` or
   `docker/` changed — the tag builds the images anyway, so a push to `master`
   does not repeat that work.
3. GitHub `codeql` scans Go and the panel's JavaScript on every push and weekly;
   `dependency-review` fails a pull request that adds a dependency with a
   high-severity advisory.
4. GitHub `release` runs on the tag. `ci` does not run on tags, so the release
   workflow repeats the checks itself — `go vet ./...` and
   `go test ./... -race -count=1 -p 1` — before it publishes anything. Then
   `goreleaser release --clean --skip=publish` builds four platforms and
   `checksums.txt` and publishes nothing, `gh release` creates the page (or
   uploads into it) and forces `--draft=false --latest`, and `docker build`/`push`
   sends `<version>-slim`, `<version>-full`, `latest` and `latest-full` to
   `ghcr.io/akrhin/mcprig`. Nothing is published by hand.
5. Gitea `images` runs on the same tag, and only on a tag — it has no manual
   trigger — and does one thing: builds `slim` and `full` and pushes them to the
   host registry from `secrets.REGISTRY`. A GitHub runner cannot push there —
   `docker login` to that registry answers `403` — so the images for the host come
   from Gitea, the release does not wait for them. Without the registry secrets,
   or without a docker socket in the runner's job container, the workflow prints
   that and exits `0`: a green `images` run does not by itself prove the images
   were pushed.
6. Release notes are the tag's `CHANGELOG.md` section, cut by
   `scripts/release-notes.sh`.

What keeps the pipeline itself safe: every action is pinned to a commit SHA with
the version in a comment, `permissions` is `contents: read` unless a job needs
more, `persist-credentials: false` keeps the checkout token out of `.git/config`,
pull requests never receive repository secrets, and the scans run their own
tools (`gitleaks`, `govulncheck`) instead of third-party actions. Public
repositories are not billed for standard runners, so the checks are not trimmed
for cost.

A red run blocks nothing by itself: neither forge gates merges on checks by
default — read the run before merging. A tag can be released by hand:
`gh workflow run release.yml -f tag=vX.Y.Z`.

## Before the tag

```bash
gofmt -l .                              # empty
go build ./... && go vet ./...
go test ./... -race -count=1 -p 1       # -p 1: manager and e2e share ports 9100-9199
scripts/sandbox-e2e.sh                  # 14/14
goreleaser check
goreleaser release --snapshot --clean   # build the archives, publish nothing
```

`goreleaser` is not installed on the development host — CI does the real build.

## Version string

`-X main.version` carries the version; a plain `go build` prints `dev`. For the
images pass `--build-arg VERSION=0.6.3` — **without** the leading `v`: a value
that starts with `v` is rejected and the build fails.

## Putting a release on a host

```bash
tar -xzf mcprig_<version>_linux_amd64.tar.gz
grep 'linux_amd64' checksums.txt | sha256sum -c -
install -m 755 mcprig ~/.local/bin/.mcprig.new
mv -f ~/.local/bin/.mcprig.new ~/.local/bin/mcprig
systemctl --user restart mcprig.service
```

Copying over a running binary fails with `ETXTBSY`; installing under another
name and renaming does not. Then `mcprig doctor` should end with
`итог: 0 проблем`, and every set should answer its version.

## See also

- [development.md](development.md) — build, tests, the live stand.
- [install.md](../install.md) — archive contents, units, images, state directory.
- [troubleshooting.md](../troubleshooting.md) — a set that does not come back after the restart.
