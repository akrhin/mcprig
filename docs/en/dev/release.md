[← Docs index](../index.md)
EN · [RU](../../ru/dev/release.md)

# Release

What happens when a `vX.Y.Z` tag is pushed, in order, plus the traps. What the
archives contain and how they are installed is in [install.md](../install.md);
this page does not repeat those numbers.

## Pipeline

1. Tag `vX.Y.Z` on `master` and push it.
2. Gitea CI (`.gitea/workflows/release.yml`) runs the checks — `go vet`, tests
   with `-race`, `govulncheck`, `gofmt` — then `goreleaser`: four platforms,
   `checksums.txt`, a **draft** release in Gitea, and the `slim`/`full` images
   pushed to the registry. Jobs are serialized (`concurrency: mcprig-release`),
   so a second tag waits instead of cancelling the first.
3. A human publishes the draft; CI does not.
4. The GitHub workflow (`.github/workflows/release.yml`) runs on the same tag:
   it rebuilds the same archives with `goreleaser --skip=publish`, builds and
   pushes the `slim`/`full` images to `ghcr.io/akrhin/mcprig`, and uploads the
   archives with `gh`, so `.goreleaser.yaml` keeps one set of
   `gitea_urls`/`draft` settings instead of two variants. It runs no tests and
   no `govulncheck`.
5. Release notes are the tag's `CHANGELOG.md` section, cut by
   `scripts/release-notes.sh`.

A tag can be released by hand: `gh workflow run release.yml -f tag=vX.Y.Z`.

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
images pass `--build-arg VERSION=0.4.0` — **without** the leading `v`: a value
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
