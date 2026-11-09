This file documents the meta-tools a client sees, with the exact response
shapes and error text.

# Meta tools

A set exposes five MCP tools. Downstream tools are not listed to the
client; they are reached through `invoke_command`.

| tool | purpose |
| --- | --- |
| `list_servers` | inventory with health and tags |
| `list_commands` | tool names of one or more servers |
| `describe_commands` | input schemas for named tools |
| `invoke_command` | call one downstream tool |
| `invoke_many` | call up to 8 independent tools in one request |

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
| `{"servers": ["alpha", "beta"]}` | object keyed by server name, each value an array; a failed server's value is `{"error": "…"}` |
| `{"prefix": "se"}` | with either form above: keep only command names starting with the prefix (case-insensitive); no match gives `[]` |
| both | `Error: укажи server или servers, не оба` |
| neither | `Error: нужен параметр server или servers` |

`prefix` filters the response after the tool list is read, so the cached schemas
stay intact and a later call without it sees every command.

With `servers`, one `requestTimeout` bounds the whole call, not each server.
Servers are queried in parallel, eight at a time.

`{"refresh": true}` (with `list_commands` and `describe_commands`) drops the
cached tool list and asks the servers again. The cache otherwise lives as long
as the connection: the set drops it when the downstream sends
`notifications/tools/list_changed`, so a tool that appears mid-connection is
visible on the next call without a refresh.

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

One failed server does not hide the others: its value is `{"error": "…"}` and
the tool returns an error only when every requested server failed.

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
| `maxBytes` | optional cap on the returned answer in bytes; default 32768, at most 1048576; `0` means the default |

Downstream arguments go **inside** `parameters`. Any other top-level key is an
error, so a call cannot silently run with empty arguments:

```text
Error: лишние ключи: path — аргументы инструмента кладутся в parameters
```

The downstream result is returned as-is: `content`, `structuredContent` and
`isError` are preserved. If the connection dies before the first response byte
(`ErrConnectionClosed`, `io.EOF`, a network error), the proxy reconnects, waits
250 ms and repeats the call once. If the response has already started or the
tool returned `isError`, there is no retry — the result is returned as it is,
so a side effect is never performed twice. `list_commands` and
`describe_commands` are never repeated: they have their own timeouts.
`requestTimeout`, when set, applies to one call.

An answer longer than `maxBytes` is cut at a UTF-8 boundary; the returned text
ends with a marker naming the kept and full sizes and the file holding the full
answer:

```text
…обрезанный текст
[обрезано: 32768 из 149873 байт, полный ответ — /tmp/mcprig-data/tool-out/1f3c9a2b7d4e5f60.txt]
```

The full answer is written `0600` to
`${MCPRIG_DATA_DIR:-$TMPDIR}/tool-out/<random>.txt`; the directory is created
on first use. An answer within the cap is returned untouched — no file, no
marker. A non-numeric or negative `maxBytes`, or one above 1048576, is an
error like any other bad argument.

## invoke_many

Input: `{"calls": [{"server": "echo", "command_name": "echo", "parameters": {"text": "a"}}, …], "maxBytes": 4096}`

| field | meaning |
| --- | --- |
| `calls` | required; 1 to 8 calls, each shaped like one `invoke_command` call |
| `maxBytes` | optional cap applied to **each** answer; same default and limits as `invoke_command` |

Calls run in parallel. At most 4 calls hit one downstream server at the same
time (eight at once made a downstream drop the batch), and at most 8 run in
total. One `requestTimeout` bounds the whole fan-out, not each call.

The answer is an array with one entry per call, **in the order of `calls`**, not
in completion order:

```json
[
  {"ok": true, "server": "echo", "command_name": "echo", "result": "echo:{\"text\":\"a\"}", "ms": 12},
  {"ok": false, "server": "nope", "command_name": "x", "error": "неизвестный сервер \"nope\"", "ms": 0}
]
```

`result` is what `invoke_command` would return: the text of a single text item,
or the whole result object otherwise, truncated at `maxBytes`. A call that
failed — unknown or disabled server, denied by permissions, transport error,
timeout, or a downstream result with `isError: true` — keeps its slot with
`ok: false` and `error`; the other calls are unaffected.

Bad input fails the whole request with `isError: true` and nothing is run: no
calls, more than 8, an unknown key inside a call, a missing `server` or
`command_name`, `parameters` that is not an object, or a bad `maxBytes`.

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

Every completed `invoke_command` — and every call inside `invoke_many` — is
counted in the set process: per client, per server and per tool, with p50/p95
and a ring of recent calls. A downstream result carrying `isError: true` counts
as a failure. The panel reads this through `GET /stats`; stdio mode does not
collect counters.

The client label comes from `_meta["io.modelcontextprotocol/clientInfo"]` of the
request being counted — the SEP-2575 per-request client info. It is written as
`name/version` (`opencode/2.2.6`) or as the bare `name` when the client sends no
version (`mcprig`). This matters for stateless HTTP, where there is no
`initialize` handshake: the label follows every request, not the session. When
`_meta` has no client info, the label falls back to the transport: `key:<6 hex
of a token hash>/<User-Agent product token>` for a keyed set, the `User-Agent`
alone without a key, and `unknown` when neither is present. Reading the
`/stats` of an open set without its token still redacts every label to
`(аноним)`.
