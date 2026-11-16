[← Docs index](index.md)
EN · [RU](../ru/alternatives.md)

# Alternatives and limits

This page says what mcprig is compared with, what each alternative does, and
where mcprig stays weaker. Every claim about another product comes from its
README or documentation; nothing was measured on a running instance. Last
activity is the `pushed_at` date taken from the GitHub API on 2026-11-16, not a
release date; re-check it before repeating the table.

Diagrammatic views: [why an endpoint](../diagrams/mcprig.why.en.html) and
[what mcprig can do](../diagrams/mcprig.capabilities.en.html).

## Without any proxy

Every client lists every server itself: N servers mean N entries in each
client's config, N tool catalogs loaded into the context of each session, keys
spread across clients, no shared policy, no shared call journal.

This is not a straw man: Claude Desktop, VS Code, Cursor and opencode each have
their own server manager with enable switches and health. What they lack is one
place for authorization, policy and observability across clients — and that is
the gap mcprig fills, in exchange for one more process per set.

## Other products

| product | kind and delivery | N servers → one endpoint | lazy catalog | per-group keys | panel | sandbox | call statistics | last activity |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| [sparfenyuk/mcp-proxy](https://github.com/sparfenyuk/mcp-proxy) | transport bridge, Python, PyPI and container | no: one backend per process; named servers are URL paths | no | not offered | no | no | no | 2026-11-15 |
| [ravitemer/mcp-hub](https://github.com/ravitemer/mcp-hub) | server manager, Node, npm | yes, one `/mcp`, tool names prefixed `filesystem__search` | no | one header token on the endpoint | not in docs: REST `/api/*` and an editor plugin; the README both claims and lists a web UI as missing | no | connection metrics, `/health` | 2025-10-24 |
| [open-webui/mcpo](https://github.com/open-webui/mcpo) | MCP to OpenAPI bridge, Python, uvx | yes, as REST sub-paths, one server per path | no: one REST route per tool | one `--api-key` for the whole proxy | no | no | no | 2026-05-17 |
| [Docker MCP Gateway](https://github.com/docker/mcp-gateway) | Go plugin for the Docker CLI, servers as containers | yes, one gateway | no | server profiles and a secrets store, not per-group tokens | Docker Desktop GUI (MCP Toolkit), no web panel | yes, container isolation | not stated | 2026-10-26 |
| [metatool-ai/metamcp](https://github.com/metatool-ai/metamcp) | aggregator, TypeScript, compose and Postgres | yes, namespaces become endpoints | no | API keys and OAuth per namespace | yes, with OIDC and tenants | not stated | an inspector and rate limits; metrics not stated | 2026-11-12, repository archived |
| [1mcp-app/agent](https://github.com/1mcp-app/agent) | aggregator, TypeScript, npm | yes, `1mcp serve` | partly: a lazy mode with `tool_list`, `tool_schema`, `tool_invoke` | not stated | not stated | not stated | not stated | 2026-11-15 |
| [lasso-security/mcp-gateway](https://github.com/lasso-security/mcp-gateway) | security gateway, Python, pip | yes, gateway tools proxy to MCP | yes, as wrapper tools | not stated; a scanning key belongs to the vendor | a dashboard in the commercial product | no | an SQLite call log | 2026-01-22 |
| [supercorp-ai/supergateway](https://github.com/supercorp-ai/supergateway) | transport bridge, Node, npx | no: one server per process | no | one `--apiKey` per instance | no | no | no | 2026-11-10 |

Where the table says "not stated", the README does not describe the feature at
all — treat it as unknown, not as absent.

## What mcprig has

- One static Go binary; no runtime, no database, no compose file.
- A set is its own process with its own port and its own Bearer key; the panel
  is a control plane and carries no MCP traffic. One set crashing leaves the
  others serving.
- Nine meta tools instead of the catalog: five work with servers
  (`list_servers`, `list_commands`, `describe_commands`, `invoke_command`,
  `invoke_many`), four serve resources and prompts. A downstream tool is never
  listed to the client.
- stdio and HTTP downstream servers; OAuth with PKCE on 401.
- Up to eight active keys per set with individual revocation, key profiles that
  narrow `allow`/`deny`, rate limits per caller and per `server/tool`, deny
  rules with a message.
- A bubblewrap sandbox for stdio servers: read-only or read-write mounts, a
  tmpfs, `RLIMIT_AS`/`NPROC`/`FSIZE` through prlimit; an untrusted server
  without a sandbox is refused.
- A panel: sets, every server, variables, keys, live calls, statistics with
  p50/p95, a ready-to-paste prompt with tool schemas, import and export of the
  registry.
- Call statistics live in the set process: a 512-call ring and a 256-sample
  reservoir, exact percentiles for the last seven days.

## Where mcprig is weaker

| limit | detail |
| --- | --- |
| one operator | a single panel password, no roles, no per-user accounts; the modes are none, password, proxy and both |
| Russian text inside | CLI help, diagnostics and log lines are Russian in this build |
| `/tool-out/<hex>` | the name is a 64-bit capability; it is not bound to the client that asked and carries no TTL |
| `X-MCPRig-Caller` | an ordinary header: a client that sets it hides its own calls from the statistics |
| CSRF | without `Origin` and without a cookie the panel accepts the request (decision 0005) |
| `GET …/authorize` | starts OAuth and a set restart, so a GET changes state (decision 0005) |
| proxy headers | with `MCPRIG_PROXY_SECRET` set, `X-Forwarded-Host` and `-Proto` are trusted without checking the peer (decision 0005) |
| login limit | counted per proxy IP, so behind a proxy five failures a minute lock out every user (decision 0005) |
| sandbox scope | CPU, the memory of the whole process subtree and network egress are not limited; `allow` is all or nothing |
| orphaned children | `SIGKILL` of a set process without a graceful shutdown leaves its stdio children running (D27) |
| invisible variable | a `${VAR}` no server mentions is stored but shown neither in the panel nor in `/api/env` (D28) |
| no typed API | a client does not see a tool schema until it calls `describe_commands` |

The full debt list with priorities is in
[the 2026-11-15 review](../history/audits/2026-11-15-review-v0.5.1.md).
