# Panel acceptance

`panel.cjs` walks the panel from an empty data directory and checks 37 things a
person would do by hand: the rail, themes, navigation, sets, servers, keys,
tools, statistics, the log, variables and import. It exits non-zero when a step
fails and prints the browser state next to the failure, so a flake is told apart
from a defect.

Requirements: Playwright and a stdio MCP server to attach (any will do).

```bash
MCPRIG_DATA_DIR=$(mktemp -d) MCPRIG_AUTH=off MCPRIG_PORT=9131 ./mcprig panel &
ACC_BASE=http://127.0.0.1:9131 ACC_DAP=/path/to/stdio-mcp node scripts/acceptance/panel.cjs
```

The panel is expected to be empty and passwordless: the script creates sets,
issues keys and deletes things.
