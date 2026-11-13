<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="design/mcprig/logo-hand-dark.png">
    <img src="design/mcprig/logo-hand-light.png" width="220" alt="mcprig">
  </picture>
</p>

# mcprig

One static Go binary that serves many MCP servers through a single MCP endpoint, with a web panel.

[![release](https://img.shields.io/github/v/release/akrhin/mcprig)](https://github.com/akrhin/mcprig/releases) [![license](https://img.shields.io/github/license/akrhin/mcprig)](LICENSE) [![go](https://img.shields.io/badge/go-1.27-blue)](go.mod)

A client that talks to a dozen MCP servers pays for a dozen tool lists in its
context before it asks anything. mcprig puts them behind five meta-tools, groups
servers into named sets, runs each set as its own process with its own port and
key, and manages the whole registry from a web panel.

- Five meta-tools instead of a whole tool catalogue
- One endpoint, one key per named set
- Registry, keys and live call tail in the panel
- Sandbox for stdio servers, OAuth for remote ones
- One static binary, no runtime dependencies

**Documentation:** [docs/en](docs/en/index.md) — install, configuration, CLI, panel,
meta-tools, architecture. Русский: [docs/ru](docs/ru/index.md).

## Quick start

Release archives cover linux and darwin on amd64 and arm64. Linux amd64:

```bash
curl -LO https://github.com/akrhin/mcprig/releases/download/v0.4.0/mcprig_0.4.0_linux_amd64.tar.gz
tar -xzf mcprig_0.4.0_linux_amd64.tar.gz
MCPRIG_PASSWORD=change-me ./mcprig panel
```

Open <http://localhost:9091>, sign in as `admin`, add a server, add a set that
contains it, press **Start**, then issue a key for the set. The panel shows the
token once: save it to `demo.key` and `chmod 600` it.

![mcprig panel](design/ui/panel.png)

*The panel: sets on their own ports and endpoints, and the servers of one set with transport and tool counts.*

Docker works the same way, with no Go toolchain needed:
`docker pull ghcr.io/akrhin/mcprig:latest` (`latest-full` adds node and python for
`npx`/`uvx` downstreams). systemd units, archive contents and the state
directory: [docs/en/install.md](docs/en/install.md).

## Connect a client

`init` prints a fragment for the client's own config file and never edits it:

```bash
./mcprig init --client claude --set demo \
  --remote http://127.0.0.1:9100/mcp --key-file ./demo.key
```

```json
{
  "mcpServers": {
    "mcprig-demo": {
      "type": "http",
      "url": "http://127.0.0.1:9100/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

`--client opencode` prints `mcp` JSON, `--client codex` prints a TOML table. For a
client that spawns the binary itself, drop `--remote` and pass
`--config ~/.config/mcprig/sets/demo/servers.json` instead: the fragment then
runs `mcprig stdio` with no port and no key. Both forms with real output:
[docs/en/cli.md](docs/en/cli.md#init).

## Architecture

[![mcprig architecture](docs/diagrams/mcprig.architecture.en.png)](docs/diagrams/mcprig.architecture.en.html)

Client → one set endpoint → five meta-tools → the downstream servers of that set.
The panel supervises one `mcprig serve` process per set, so a restart or a crash
of one set leaves the others serving. The interactive version of the picture
(themes, zoom, PNG/SVG export) is
[docs/diagrams/mcprig.architecture.en.html](docs/diagrams/mcprig.architecture.en.html);
the spec it is generated from is next to it as `.json`.

## Configuration

A set is one `servers.json` in the lazy-mcp format, read as-is: `servers[]` with
`command` or `url`, plus `env`, `headers`, `sandbox`, `transport`,
`permissions`. The panel writes the same file for the sets it runs, so a config
made by hand and one made in the browser are not two formats.

`"sandbox": {}` puts a stdio server under `bubblewrap`: read-only root, no
`/home`, no network, `prlimit` limits. A server tagged `untrusted` cannot start
without it. Field reference and what the sandbox does not limit:
[docs/en/configuration.md](docs/en/configuration.md).

## Security defaults

A set with no `authToken` refuses to start unless it is explicitly open; the
panel password is constant-time compared with rate limiting per IP; tokens are
returned in full once and as a prefix afterwards. Auth modes, host policy,
permission rules, limits and the 4 MiB body cap:
[docs/en/configuration.md](docs/en/configuration.md) and
[docs/en/panel.md](docs/en/panel.md).

## Status

Pre-1.0, single operator: one panel, one password, no per-user roles. CLI help
and log lines are Russian.

Issues and pull requests follow [CONTRIBUTING.md](CONTRIBUTING.md). Security
reports: [SECURITY.md](SECURITY.md).

## License

MIT, see [LICENSE](LICENSE).

---

[English](README.md) · [Русский](README.ru.md)
