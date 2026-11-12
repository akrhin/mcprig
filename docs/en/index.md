EN · [RU](../ru/index.md)

# mcprig documentation

mcprig is a single static binary that puts many MCP servers behind one endpoint:
a client sees five meta-tools instead of every tool of every server, and each
*set* runs as its own supervised process with its own port and key. The same
binary also carries the registry, the supervisor and the web panel.

Everything below is written against the shipped binary. Numbers, flag names and
error strings are the real ones; if a page and the binary disagree, the binary
wins — open an issue.

## Three ways in

**Put it up.** Take a release archive or an image, install the systemd unit,
then run `mcprig doctor` once: it checks the config, the sets, the keys, the
secrets and the listening ports in one pass.
→ [install.md](install.md), then [cli.md#doctor](cli.md#doctor).

**Configure and connect.** One `servers.json` describes a set; the panel edits
it too. Get a client fragment with `mcprig init`, hand it the set's key, and the
client will see the meta-tools.
→ [configuration.md](configuration.md) → [cli.md#init](cli.md#init) →
[meta-tools.md](meta-tools.md).

**Develop.** Build, run the test gate (`-p 1` is not optional), drive the live
stand against real servers, then cut a release.
→ [dev/development.md](dev/development.md) → [dev/release.md](dev/release.md).

## Pages

| page | what is inside |
| --- | --- |
| [install.md](install.md) | release archives and what they contain, systemd user units, Docker `slim`/`full`, the state directory on disk |
| [configuration.md](configuration.md) | every field of `servers.json`: servers, `sandbox`, secrets, `transport`, `permissions`, `oauth`, and what happens to a broken entry |
| [cli.md](cli.md) | the six subcommands with real output: `serve`, `stdio`, `panel`, `doctor`, `warm`, `init`; `tool-out` retention |
| [panel.md](panel.md) | what the panel shows, the HTTP API behind it, call history, health, and how access is checked |
| [meta-tools.md](meta-tools.md) | the five tools a client sees, exact response shapes, error text, secret scrubbing, counters |
| [architecture.md](architecture.md) | how the processes fit together, why each set is its own process, health, statistics, OAuth |
| [troubleshooting.md](troubleshooting.md) | symptom → cause → what to do, from the incidents that actually happened |
| [glossary.md](glossary.md) | set, meta-tool, downstream/upstream, tool-out, sandbox, hook, warm |
| [dev/development.md](dev/development.md) | requirements, build, the test gate, the contract suite, the live stand |
| [dev/release.md](dev/release.md) | tag → CI → archives → mirror, as a checklist |

The live stand against real MCP servers is described in
[dev-real-servers.md](../dev-real-servers.md) — internal notes, in Russian.

## Archive

[history/](../history/index.md) holds the process artefacts: the Go rewrite plan,
decisions, proposals, audits, research and the frozen contracts of the rewrite
era. It is a record of *why* things were done, not documentation of how the
product behaves today — those pages are not updated. History is in Russian.

## Language

Every product page exists in English (the canonical text) and Russian
(`../ru/`, a translation of the same page). `docs/CURRENT-GOAL.md` and
everything under `history/` are internal and stay in Russian.
