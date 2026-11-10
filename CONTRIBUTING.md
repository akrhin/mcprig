# Contributing

mcprig is pre-1.0 and single-operator. Gitea (`sintez/mcprig`) is the source of
truth; GitHub (`akrhin/mcprig`) mirrors release archives.

Read before you write: `AGENTS.md` (repository rules), `docs/CURRENT-GOAL.md`
(the stage and its gate), `docs/en/development.md` (build, tests, live stand).
Behaviour comes from `docs/reference/ts-manager-contract.md`; a change that
contradicts it needs a record in `docs/decisions/`.

## Where things go

| What | Where |
| --- | --- |
| a bug or a proposal | an issue here |
| a security problem | privately to the maintainer, never a public issue |
| a question about setup | docs first: `docs/en/`, `docs/ru/` |
| a change | a pull request from a branch, never a commit on `master` |

## Issues

One problem per issue; search the closed ones first.

- Title: `area: what is wrong`, for example
  `sandbox: prlimit is missing on alpine, every boxed server fails`.
  Areas: `panel`, `set`, `stdio`, `sandbox`, `config`, `cli`, `docs`, `docker`.
- The body must say what happened, what you expected, how to reproduce it
  (commands and a config with secrets replaced by `<token>`), the version
  (`mcprig --version` or the image tag) and the exact error line. Error texts
  are Russian and are searched for in the code, so paste them, do not retype.
- The issue form asks for these fields; an issue without a version and an
  error line gets sent back for them.

## Pull requests

- Branch: `<prefix>/<short-name>`, for example `openchamber/panel-isolation`.
- Commits: Conventional Commits with a Russian subject —
  `type(scope): subject`. Types: `feat`, `fix`, `docs`, `test`, `refactor`,
  `perf`, `chore`, `release`. One commit carries one meaningful change.
- PR title = the change in one line, same form as a commit: Gitea turns it
  into the merge commit.
- PR body: what changed and why, the gate you ran and its result, the docs and
  changelog you touched, and `Closes #N`.
- `git commit` without `--no-verify`: the hook builds the tree and runs the
  tests.

The gate before pushing:

```bash
gofmt -l .                          # must print nothing
go build ./...
go vet ./...
go test ./... -race -count=1 -p 1   # -p 1 is required
```

`-p 1` is not optional: `internal/manager` and `e2e` start real sets on the
shared 9100–9199 range and take each other's ports when packages run in
parallel. The contract suite runs against a live panel:

```bash
E2E_BASE=http://127.0.0.1:9091 ./scripts/contract-e2e.sh
```

A behaviour change updates `docs/en/` and `docs/ru/` together and adds a line
under `## [Unreleased]` in `CHANGELOG.md`. If no doc update is needed, say so in
the PR body instead of leaving it unsaid.

## Code

- Go standard style; a new dependency needs its reason in the PR.
- Comments explain the why the code cannot show, one line each.
- Non-trivial logic — a branch, a loop, a parser, anything touching tokens,
  auth or the sandbox — arrives with a test.
- Defects of the TypeScript reference are requirements to fix, not behaviour
  to copy: no line of TypeScript is ported here.

## Versioning

[SemVer](https://semver.org/), `MAJOR.MINOR.PATCH`, in a git tag `vX.Y.Z`. The
version comes from the tag alone: goreleaser injects it via
`-X main.version={{ .Version }}`, a build from source prints `dev`, and
`mcprig --version` (or the panel's own version) shows what a binary carries.
There is no version constant in the tree, so nothing to bump by hand.

Pre-1.0 the numbers mean:

| Change | Bump |
| --- | --- |
| a fix that keeps every contract, config key and meta-tool schema as it was | `PATCH` |
| a new meta-tool, config key, flag, panel surface, or a behaviour change an operator can notice | `MINOR` |
| a breaking change — a renamed or dropped config key, an incompatible meta-tool or `/mcp` contract, a changed auth default | `MINOR` while pre-1.0, `MAJOR` after 1.0 |
| docs, tests, CI, refactor with no observable change | `PATCH`, or no release at all |

A breaking change ships in a `MINOR` only while the project is pre-1.0, and it
must say so in its changelog section and carry a migration note. After 1.0 no
breaking change rides in a minor.

Container tags follow the release: `X.Y.Z-slim` and `X.Y.Z-full`, plus
`latest` and `latest-full` pointing at the newest release — never at a branch.

Rules:

- A tag is cut from a `release: vX.Y.Z` commit that moves `## [Unreleased]`
  into `## [X.Y.Z] - YYYY-MM-DD` and leaves `Unreleased` empty. Nothing else
  changes in that commit.
- A published tag is never moved and never deleted; a broken release gets a new
  `PATCH`.
- `CHANGELOG.md` is the only release-notes source: Added / Changed / Fixed /
  Removed / Security, highest impact first.

## Release

A `v*` tag runs gofmt, vet, the race suite and govulncheck, then goreleaser
builds the archives into a draft; a human publishes it. The GitHub mirror
receives the same archives. Release notes are the tag's `CHANGELOG.md` section,
cut from `## [Unreleased]` by a `release:` commit.
