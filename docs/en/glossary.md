[← Docs index](index.md)
EN · [RU](../ru/glossary.md)

# Glossary

The words this documentation uses in a narrow sense, with the place they come
from. Where a term is a config key, the key is spelled as in the file.

**set** (also *набор*) — a named group of MCP servers published at one endpoint
with one port and one key. A set is configured by a single `servers.json` and runs
as its own process (`mcprig serve --config …`), supervised by the panel. All five
meta-tools belong to a set, and the same server may appear in two sets with two
independent processes. See [configuration.md](configuration.md#top-level) and
[architecture.md](architecture.md#one-process-per-set).

**meta-tool** — one of the five `tools/list` entries a client of a set sees:
`list_servers`, `list_commands`, `describe_commands`, `invoke_command`,
`invoke_many`. The tools of the servers behind the set are never listed to the
client; they are reached through `invoke_command`. See
[meta-tools.md](meta-tools.md).

**downstream** — the actual MCP server behind the proxy: a stdio process mcprig
spawns, or a remote HTTP endpoint. The Go package is `internal/downstream`. The
same server is called **upstream** where the direction of one call matters (the
server sits upstream of the hop); here the two words name the same thing.

**tool-out** — `${MCPRIG_DATA_DIR:-$TMPDIR}/tool-out/`, where an answer cut by
`maxBytes` is written in full, and the path the truncation marker names. Files
live 24 hours; the directory is capped at 256 MiB, oldest first. Each set has its
own `sets/<id>/tool-out/`. See [cli.md#tool-out-retention](cli.md#tool-out-retention).

**sandbox** — the `sandbox` block of a server entry: `bwrap` (bubblewrap) plus
`prlimit`, giving the stdio server its own mount tree, a read-only root, no
`/home` and no host network. `mode: bwrap` by default, `mode: none` turns it off;
`onUnavailable` decides what happens when bubblewrap cannot run — `deny`
(fail-closed, the default), `warn` or `allow`. A server tagged `untrusted` accepts
only `deny`. See [configuration.md#sandbox](configuration.md#sandbox).

**hook** — a client-side plugin that changes what the agent is told, not what the
proxy does. In this project the word means the route hook: an opencode plugin
(`mcprig-route.ts`) that puts the line "MCP lives behind mcprig" into the agent's
context so it stops looking for individual servers. Its design and the option
that was chosen are in the archive,
[history/proposals/0001-route-hook.md](../history/proposals/0001-route-hook.md).

**warm** — preloading a set's tool cache before a client asks: `mcprig warm` calls
`list_servers`, then one `list_commands` *without* `refresh` (which would clear
the cache instead). The next client call returns in milliseconds instead of
waiting for every downstream to unpack. `deploy/mcprig-warm.timer` repeats it
every ten minutes. See [cli.md#warm](cli.md#warm).

**key** — the bearer token a closed set requires; `mcprig init` prints the client
fragment that carries it. Counter labels use the key's note when the owner has
named the key, otherwise `key:<6 hex>/<User-Agent>`: a short fingerprint of the
key plus the User-Agent product token. See
[meta-tools.md#counters](meta-tools.md#counters).

**open set** — a set with `transport.allowAnonymous: true`: no `Authorization`
header is needed. It also *rejects* any header it is given, because it has no token
to compare against.

**soft fail** — how one broken server entry is handled: the server keeps its
`_configError`, is reported as `healthy: false`, and the rest of the set starts.
Deliberate — see [configuration.md#soft-fail](configuration.md#soft-fail).

**state directory** — `MCPRIG_DATA_DIR`, by default `~/.local/state/mcprig`
created `0700`: `registry.json`, `env.json`, `manager.pid`, and per set
`sets/<id>/{servers.json,mcprig.log,oauth.json,stats,tool-out}`. See
[install.md#state-on-disk](install.md#state-on-disk).

## See also

- [architecture.md](architecture.md) — how these pieces run as processes.
- [configuration.md](configuration.md) — the fields named above, in full.
- [meta-tools.md](meta-tools.md) — the five tools, with exact response shapes.
