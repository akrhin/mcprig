This file covers building, the test gate, the contract suite, and the live
stand against real MCP servers.

# Development

## Requirements

- Go 1.27 (`go.mod` asks for `1.27.3`; with `GOTOOLCHAIN=auto` the toolchain is
  downloaded on the first build).
- `bash` and `curl` for the scripts.
- Optional for the live stand: serena on `127.0.0.1:8899`, `npx`, and
  `xerj` in `~/.local/bin`.

## Build and test

```bash
go build ./...
go vet ./...
go test ./... -race -count=1 -p 1
```

The race-enabled suite is the stage gate. It covers config loading, both
transports, the five meta-tools, permissions, OAuth against a fake authorization
server, HTTP transport rules, the manager API, supervision and statistics.

`-p 1` is required: `internal/manager` and `e2e` start real sets on the shared
9100–9199 range and take each other's ports when packages run in parallel.

Useful narrower runs:

```bash
go test ./internal/config -run TestLoad -v
go test ./e2e -run TestLifecycle -v   # panel + set + stdio downstream, no setup
```

`e2e/lifecycle_test.go` builds the real binary and uses
`internal/manager/testdata/fakeset` as a downstream, so it needs no external
services. It is the fastest way to see the whole chain work.

## Contract suite

`scripts/contract-e2e.sh` runs `go test ./e2e -run TestContract` against a live
manager:

```bash
E2E_BASE=http://127.0.0.1:9091 ./scripts/contract-e2e.sh
```

| variable | default | meaning |
| --- | --- | --- |
| `E2E_BASE` | `http://127.0.0.1:9091` | panel under test |
| `E2E_STRICT` | `1` | `1` fails on known reference defects; `0` downgrades them to log lines |
| `E2E_AUTH` | unset | `1` also checks that the API requires auth; needs a password-protected panel |

The same suite runs against the TypeScript reference (`E2E_STRICT=0`) and
against this binary (`E2E_STRICT=1`). The checks are derived from
[the TS manager contract](../reference/ts-manager-contract.md) (Russian).

An empty `E2E_BASE` skips the suite; with `E2E_STRICT=1` it exits non-zero so a
skipped run cannot read as a passed gate.

## Live stand

`scripts/dev-real-set.sh` builds the binary, starts a panel with auth off, adds
three real servers, creates an open set and starts it:

```bash
bash scripts/dev-real-set.sh
# panel:  http://127.0.0.1:9177   (auth off, data /tmp/mcprig-dev-real)
# set:    http://127.0.0.1:9101/mcp  (open, no key)
# log:    /tmp/mcprig-dev-real/sets/real/mcprig.log
bash scripts/dev-real-set.sh stop
```

| variable | default | meaning |
| --- | --- | --- |
| `PANEL_PORT` | `9177` | panel port |
| `SET_PORT` | `9101` | set port |
| `DATA` | `/tmp/mcprig-dev-real` | state directory, pid file and binary |

The script registers:

| server | how it connects |
| --- | --- |
| `serena` | `http://127.0.0.1:8899/mcp` |
| `socraticode` | `npx -y socraticode@1.16.1` |
| `xerj` | `$HOME/.local/bin/xerj mcp` |

It needs those three to be reachable; otherwise the set still starts and those
servers show as unhealthy. The printed example calls `activate_project` through
`invoke_command` and returns the repository root. `stop` kills the panel by its
pid file; the set exits with the panel.

Logs: the panel writes `$DATA/panel.log`, the set writes
`$DATA/sets/real/mcprig.log`.

## Release build

`.goreleaser.yaml` is the release definition (`goreleaser release --snapshot
--clean` to check locally). CI builds and publishes the archives on a `v*` tag
(Gitea, mirrored to GitHub); release notes are the tag's `CHANGELOG.md` section
via `scripts/release-notes.sh`. `goreleaser` itself is not installed on the
development host.
