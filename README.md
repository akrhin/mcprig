<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="design/mcprig/logo-hand-dark.png">
    <img src="design/mcprig/logo-hand-light.png" width="220" alt="mcprig">
  </picture>
</p>

[English](README.md) · [Русский](README.ru.md)

# mcprig

One static Go binary that serves many MCP servers through one endpoint, with a
web panel.

## What it is

mcprig is three things in one binary:

- a **proxy** that exposes four meta-tools (`list_servers`, `list_commands`,
  `describe_commands`, `invoke_command`) instead of loading every downstream
  tool into the client's context;
- a **manager** that keeps the registry of servers and sets, supervises one
  process per set, and issues access keys;
- a **web panel** for the same registry and the live call tail.

A **set** is a named group of downstream servers published as one MCP endpoint.
Each set runs as its own `mcprig serve` process on its own port with its own
bearer key (or explicitly open), so a restart or a crash of one set does not
touch the others.

`servers.json` is the lazy-mcp format, read as-is: `servers[]`, both `command`
forms, `url`, `env`, `headers`, `transport`, `healthMonitor`,
`requestTimeout`. A server with a broken field stays in the list as unhealthy
with its error; a missing name or invalid JSON fails the load.

## Quick start

Go 1.25 is required; `GOTOOLCHAIN=auto` downloads it on the first build.

```bash
# 1. build
go build -o mcprig ./cmd/mcprig

# 2. run the panel (loopback only, no login on this local demo)
MCPRIG_AUTH=off ./mcprig panel
# from another shell:
curl -s http://127.0.0.1:9091/healthz
# {"sets":0,"status":"ok"}
```

In the panel, add a server, add a set containing it, press **Start**, then issue
a key (**Keys** → issue). The token is shown once; save it to `demo.key`
(`chmod 600`). Then print a client fragment:

```bash
# 3. connect a client
./mcprig init --client opencode --set demo \
  --remote http://127.0.0.1:9100/mcp --key-file ./demo.key
```

```json
{
  "mcp": {
    "mcprig-demo": {
      "enabled": true,
      "headers": {
        "Authorization": "Bearer <token>"
      },
      "type": "remote",
      "url": "http://127.0.0.1:9100/mcp"
    }
  }
}
```

`--client claude` prints `mcpServers` JSON for `claude_desktop_config.json`;
`--client codex` prints TOML for `~/.codex/config.toml`. For a stdio client
(the client spawns the binary), pass `--config <servers.json>` instead of
`--remote`. `init` writes nothing to disk.

![mcprig panel](design/ui/panel.png)

## CLI

| command | what it does | example |
| --- | --- | --- |
| `serve` | runs one set from `servers.json` as an HTTP MCP endpoint | `mcprig serve --config ./servers.json` |
| `stdio` | serves one set over stdin/stdout; the client owns the process | `mcprig stdio --config ./servers.json` |
| `panel` | runs the manager and the web panel | `MCPRIG_PASSWORD=... mcprig panel` |
| `init` | prints a client config fragment; writes no files | `mcprig init --client claude --set demo --remote http://127.0.0.1:9100/mcp --key-file ./demo.key` |

Flags, verbatim:

| command | flags |
| --- | --- |
| `serve` | `-config` (default `os.UserConfigDir()/mcprig/servers.json`), `-addr`, `-version` |
| `stdio` | `-config` (same default) |
| `panel` | none; configured through `MCPRIG_*` environment variables |
| `init` | `-client` (default `opencode`), `-set` (default `demo`), `-remote`, `-key`, `-key-file`, `-binary` (default `mcprig`), `-config` |

Flags accept one or two dashes (`-config` and `--config` are the same).
Exit codes: `0` for help, `2` for a command-line error, `1` for everything
else. Details and examples: [docs/en/cli.md](docs/en/cli.md).

## Configuration

`servers.json` is the same file lazy-mcp reads. A minimal set:

```json
{
  "servers": [
    {
      "name": "fs",
      "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/srv/docs"],
      "env": {"LOG_LEVEL": "warn"},
      "tags": ["files", "local"]
    },
    {
      "name": "serena",
      "url": "http://127.0.0.1:8899/mcp",
      "headers": {"Authorization": "Bearer ${SERENA_TOKEN}"},
      "tags": ["code"]
    },
    {
      "name": "db",
      "command": ["/usr/local/bin/db-mcp"],
      "env": {"DB_PASSWORD": "{file:/run/secrets/db_password}"},
      "enabled": false
    }
  ],
  "transport": {
    "type": "http",
    "host": "127.0.0.1",
    "port": 9100,
    "path": "/mcp",
    "authToken": "{file:/home/me/.config/mcprig/demo.token}",
    "allowedHosts": ["127.0.0.1", "localhost"]
  },
  "permissions": {
    "default": "allow",
    "rules": [
      {"server": "db", "pattern": "drop_*", "action": "deny", "message": "no destructive SQL"}
    ]
  },
  "requestTimeout": 120000
}
```

`${VAR}` is taken from the environment of the set process; `{file:/absolute}`
reads a secret file and strips one trailing newline. `serve` refuses to start
without `transport.authToken` unless `transport.allowAnonymous` is true. Full
field reference: [docs/en/configuration.md](docs/en/configuration.md).

## Meta tools

| tool | what it returns |
| --- | --- |
| `list_servers` | every server with `type`, `enabled`, `healthy`, tags; optional `filter` over name, description and tags |
| `list_commands` | commands of one server (`server`) as an array, or of several (`servers`) as an object keyed by name |
| `describe_commands` | input schemas for the named commands; unknown names land in `missing` |
| `invoke_command` | runs `command_name` on `server`; downstream inputs go inside `parameters` |

All four are thin wrappers over the downstream MCP servers. Response forms:
[docs/en/meta-tools.md](docs/en/meta-tools.md).

## Security defaults

| area | default |
| --- | --- |
| set bearer | compared in constant time; an empty `authToken` refuses to start unless the set is explicitly open |
| panel password | plain text or `scrypt$N$r$p$salt$hash`; constant-time compare; 5 failed logins per minute per IP, one scrypt at a time |
| panel session | `mcp_session` cookie: random id signed with HMAC-SHA256, 12 h TTL, `HttpOnly`, `SameSite=Lax`, `Secure` over TLS; sessions live in memory, so a restart logs everyone out |
| CSRF | writes reject `Sec-Fetch-Site: cross-site`, require `Origin` to match `Host`, and require a JSON body |
| Host policy | empty `allowedHosts` / `MCPRIG_ALLOWED_HOSTS` means loopback only; `*` disables the check |
| tokens | the panel and API return only the 8-character prefix; the full token is shown once, when a key is issued |
| `/stats` | on an open set, client labels become `(аноним)` and error text is dropped |
| request body | 4 MiB cap on the MCP endpoint |

## Install

Release archives are built by `.goreleaser.yaml` for linux and darwin on amd64
and arm64 (`mcprig_<version>_<os>_<arch>.tar.gz`, plus `checksums.txt`). The
config is written; `goreleaser` is not installed on the development host, so
the archive build itself has not been verified.

- systemd user units: `deploy/mcprig.service` (panel) and
  `deploy/mcprig-set@.service` (one set per unit).
- Docker: `slim` (binary only, HTTP downstreams) and `full` (adds node and
  python for `npx`/`uvx` stdio servers). The images were not built on this
  host because the registry was unreachable.

Details: [docs/en/install.md](docs/en/install.md).

## Development

```bash
go build ./...
go vet ./...
go test ./... -race -count=1
E2E_BASE=http://127.0.0.1:9091 ./scripts/contract-e2e.sh
bash scripts/dev-real-set.sh
```

`dev-real-set.sh` builds the binary and starts a panel plus one open set against
real serena, socraticode and xerj instances; `bash scripts/dev-real-set.sh stop`
shuts it down. More: [docs/en/development.md](docs/en/development.md).

## Status

Pre-1.0, single operator: one panel, one password, no per-user roles. Stages
0–4 are accepted (config, proxy core, OAuth, HTTP transport, manager); stage 5
(the panel) is in progress. CLI help and log lines are Russian in this build.

License: MIT, see [LICENSE](LICENSE).
