This file documents the four meta-tools a client sees, with the exact response
shapes and error text.

# Meta tools

A set exposes exactly four MCP tools. Downstream tools are not listed to the
client; they are reached through `invoke_command`.

| tool | purpose |
| --- | --- |
| `list_servers` | inventory with health and tags |
| `list_commands` | tool names of one or more servers |
| `describe_commands` | input schemas for named tools |
| `invoke_command` | call one downstream tool |

Every error below is returned as a normal tool result with `isError: true` and
one text item whose text starts with `Error: `. The MCP call itself does not
fail.

## list_servers

Input: `{"filter": "code"}` (`filter` optional). The filter is matched
case-insensitively against name, description and tags **before** any
connection: a filtered-out server is never dialed.

```json
{
  "servers": [
    {
      "name": "echo",
      "type": "stdio",
      "enabled": true,
      "healthy": true,
      "tags": ["fixture"]
    }
  ],
  "version": "dev",
  "pid": 666441
}
```

| field | meaning |
| --- | --- |
| `name` | server name |
| `description` | present when the config has one |
| `type` | `stdio` or `remote` |
| `enabled` | from the config; a disabled server is not probed and is reported `healthy: true` |
| `healthy` | result of a probe; `false` for a broken config or a failed connect |
| `error` | probe error text, present with `healthy: false` |
| `tags` | from the config, when present |
| `version` | build version of the proxy |
| `pid` | proxy process id |

Health probes run concurrently, at most 8 at once, with a 15 s timeout and a
15 s cache that also remembers failures.

## list_commands

Input is one of two forms:

| input | output |
| --- | --- |
| `{"server": "alpha"}` | array of commands |
| `{"servers": ["alpha", "beta"]}` | object keyed by server name, each value an array |
| both | `Error: укажи server или servers, не оба` |
| neither | `Error: нужен параметр server или servers` |

```json
[
  {
    "server": "echo",
    "name": "echo",
    "command": "echo",
    "description": "Echo the given text back."
  }
]
```

`name` is the canonical field; `command` repeats it for clients written against
the older shape. `description` is present when the server provides one.
Pagination is followed to the end, capped at 20 pages.

## describe_commands

Input: `{"server": "echo", "command_names": ["echo", "nope"]}`; both fields are
required.

```json
{
  "commands": [
    {
      "name": "echo",
      "description": "Echo the given text back.",
      "inputSchema": {
        "type": "object",
        "properties": {"text": {"type": "string"}},
        "required": ["text"]
      }
    }
  ],
  "missing": ["nope"]
}
```

`missing` is always present and lists names the server does not expose; they do
not fail the call. `outputSchema` appears when the downstream tool declares
one.

## invoke_command

Input:

```json
{
  "server": "echo",
  "command_name": "echo",
  "parameters": {"text": "hello"}
}
```

| field | meaning |
| --- | --- |
| `server` | required |
| `command_name` | required |
| `parameters` | optional object; `null` and an empty string mean `{}` |

Downstream arguments go **inside** `parameters`. Any other top-level key is an
error, so a call cannot silently run with empty arguments:

```text
Error: лишние ключи: path — аргументы инструмента кладутся в parameters
```

The downstream result is returned as-is: `content`, `structuredContent` and
`isError` are preserved. If the downstream closes the session
(`ErrConnectionClosed`), the proxy reconnects once and repeats the call; a
second failure is returned with its reason. `requestTimeout`, when set,
applies to one call.

## Permissions

The allow/deny block is checked before the server is connected; see
[configuration.md](configuration.md#permissions). A denied call returns:

```json
{
  "isError": true,
  "content": [{"type": "text", "text": "🚫 Blocked by mcprig policy: no destructive SQL"}],
  "_meta": {"lazyMcpPolicy": "blocked"}
}
```

The `_meta.lazyMcpPolicy` marker is what host hooks look for.

## Counters

Every completed `invoke_command` is counted in the set process: per client, per
server and per tool, with p50/p95 and a ring of recent calls. A downstream
result carrying `isError: true` counts as a failure. The panel reads this
through `GET /stats`; stdio mode does not collect counters.
