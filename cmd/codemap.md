# cmd/

## Responsibility

Process entry points of the module: `cmd/mcprig` is the only shipped binary
(serve, stdio, panel, and the operator CLI), `cmd/testmcp` is a test fixture.

## Design

| Directory | Responsibility | Map |
| --- | --- | --- |
| `cmd/mcprig/` | The binary: stdlib-`flag` subcommand dispatch, set process supervision, panel, CLI helpers (`doctor`, `init`, `auth`, `approve`, `status`, `logs`, `warm`) | [View Map](mcprig/codemap.md) |
| `cmd/testmcp/` | Fixture stdio MCP server with an `echo` tool and a deliberately failing `fail` tool, used by lifecycle tests | [View Map](testmcp/codemap.md) |

There is no shared code between the two: a fixture must stay trivial, so nothing
is factored out of `cmd/mcprig` for it.

## Flow

`main` → `parseArgs` → one `run*` function per subcommand (`cmd/mcprig/main.go:128`).
Everything below the command layer lives in `internal/`.

## Integration

`cmd/mcprig` wires the internal packages together and owns the process lifecycle;
`.goreleaser.yaml` and `Dockerfile` build only its binary. `cmd/testmcp` is
referenced from tests in `e2e/` and the internal packages, never from release
packaging.
