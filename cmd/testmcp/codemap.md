# cmd/testmcp/

## Responsibility

A fixture stdio MCP server for the lifecycle tests: it starts fast, answers
`tools/list`, exposes two tools and serves on the stdio transport
(`main.go:1-2,18-35`). It is not shipped in releases; `deploy/` and `.goreleaser.yaml`
build only `cmd/mcprig`.

## Design

- One `mcp.Server` named `testmcp`, version `0.0.1` (`main.go:19`), built with the
  go-sdk's typed `mcp.AddTool` helper.
- `echo` (`main.go:21-24`) — input struct `echoIn{Text string}` (`main.go:14-16`),
  returns the text back as `TextContent`.
- `fail` (`main.go:26-29`) — always returns `errors.New("fixture failure")`, so a
  test can tell a tool-level error from a transport failure.
- Transport is `mcp.StdioTransport` (`main.go:31`); a run error is logged and the
  process exits 1.

## Flow

Spawned as a child process by a test (via a set's `servers.json`, see
`internal/downstream/`). Reads JSON-RPC on stdin, writes on stdout, stderr is the
log sink. No arguments, no config, no files touched.

## Integration

Fixture only: `e2e/` and `internal/downstream`/`internal/mcpserver` tests point a
set at this binary. Deliberately trivial so a failing test is never the fixture's
fault — keep it that way.
