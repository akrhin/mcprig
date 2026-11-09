# opencode plugin: route MCP calls through mcprig

A small plugin that appends one constant line to the system prompt: MCP servers
are reachable through `mcprig`, and the way in is its meta-tools.

It names **only** mcprig's own meta-tools (`list_servers`, `list_commands`,
`describe_commands`, `invoke_command`). The servers behind the proxy are never
mentioned, so the file is the same for every set and every host, and it does not
go stale when a set is edited.

## Install

```bash
cp integrations/opencode/mcprig-route.ts ~/.config/opencode/plugins/
```

Then restart the host that runs the plugin (the OpenChamber instance, or your
CLI session) — a plugin file is read at startup.

Check that it loaded:

```bash
grep 'loading plugin.*mcprig-route' ~/.local/share/opencode/log/opencode.log
```

`opencode plugin list` is not a check here: it reports the server the CLI is
connected to, which may be a different process.

## Disable

```bash
export MCPRIG_ROUTE=off
```

or delete the file from the plugins directory. Without mcprig the hint is
harmless but useless, so delete it rather than leaving it on.

## Notes

- The text is constant by design: it is appended on every step, and a variable
  part would change the request prefix and drop the prompt cache.
- Two hooks are registered — `context` and `compaction` — otherwise the rule
  disappears exactly on the request that rewrites the context.
