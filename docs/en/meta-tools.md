[← Docs index](index.md)
EN · [RU](../ru/meta-tools.md)

This file documents the meta-tools a client sees, with the exact response
shapes and error text.

# Meta tools

A set exposes nine MCP tools: five that work with downstream servers, plus
four for their resources and prompts. Downstream tools themselves are not
listed to the client; they are reached through `invoke_command`.

The four resource and prompt tools (`list_resources`, `read_resource`,
`list_prompts`, `get_prompt`) answer only for a server whose config names them
in `expose`; see [Resources and prompts](#resources-and-prompts) below.

| tool | purpose |
| --- | --- |
| `list_servers` | inventory with health and tags |
| `list_commands` | tool names of one or more servers |
| `describe_commands` | input schemas for named tools |
| `invoke_command` | call one downstream tool |
| `invoke_many` | call up to 8 independent tools in one request |
| `list_resources` | resources of the servers that expose them |
| `read_resource` | read one resource |
| `list_prompts` | prompts of the servers that expose them |
| `get_prompt` | render one prompt with arguments |

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
| `healthy` | result of a probe; `false` for a broken config or a failed connect. A probe that could not get a free slot is reported `true` and is not cached — see `unknown` |
| `unknown` | `true` when the probe never started (no free slot): `healthy: true` here means "not sick", not "checked" |
| `error` | probe error text, present with `healthy: false` |
| `isolation` | `bwrap` when the sandbox is applied; `без изоляции: <причина>` when it was requested but bubblewrap could not start (`onUnavailable: warn`/`allow`); `нет: конфиг отвергнут` when the record has a `_configError`; absent only for a valid server without a `sandbox` block |
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
| `{"namesOnly": true}` | with either form above: names only — an array of strings, or arrays keyed by server. A large server returns hundreds of tools and tens of KB of catalog; names cost about a sixth of that, and `describe_commands` gives one schema when needed |
| `{"maxBytes": 4096}` | with either form above: cap the answer, limit 1048576. Unlike `invoke_command` and `invoke_many`, catalog calls have **no default cap**: without the field, or with `0`, the whole catalog arrives; over an explicit cap the answer goes to a file and the marker says how much arrived |
| both | `Error: укажи server или servers, не оба` |
| neither | `Error: нужен параметр server или servers` |

`prefix` filters the response after the tool list is read, so the cached schemas
stay intact and a later call without it sees every command.

A key issued with a profile (see [configuration.md](configuration.md#profiles))
gets a catalog without the commands the profile refuses — `deny: ["*"]` leaves
it empty. A key without a profile sees the whole set, as before.

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
one. `maxBytes` works here too and, like `list_commands`, only when given: a
batch of schemas over the cap goes to a file with the same truncation marker.

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
| `command_name` | one of `command_name` or its alias `command` (see below) |
| `command` | alias for `command_name`; `list_commands` returns the name in the `command` field, so either key works. Sending both with different values is an error. The client sees the schema at session start, so after the update it must re-read `tools/list` (or start a new session) — otherwise the old `required: command_name` wins over `anyOf` |
| `parameters` | optional object; `null` and an empty string mean `{}` |
| `maxBytes` | optional cap on the returned answer in bytes; default 32768, at most 1048576; `0` means the default. The catalog tools `list_commands` and `describe_commands` are the exception: they cut nothing unless `maxBytes` is given |

Downstream arguments go **inside** `parameters`. Any other top-level key is an
error, so a call cannot silently run with empty arguments:

```text
Error: лишние ключи: path — допустимые: server, command_name, command, parameters, maxBytes; аргументы инструмента кладутся в parameters
```

A wrong JSON type is named by key and kind, without Go types:
`Error: ключ server: ожидался тип «строка», получен тип «число»`. The
`аргументы:` prefix appears only when the type error is not tied to a named
key — an unknown or mis-shaped top-level field, e.g.
`Error: аргументы: ожидался тип «объект», получен тип «строка»`.

The downstream result is returned as-is: `content`, `structuredContent` and
`isError` are preserved. If the connection dies before the first response byte
(`ErrConnectionClosed`, `io.EOF`, a network error), the proxy reconnects, waits
250 ms and repeats the call once. If the response has already started or the
tool returned `isError`, there is no retry — the result is returned as it is,
so a side effect is never performed twice. `list_commands` and
`describe_commands` are never repeated: they have their own timeouts.
`requestTimeout`, when set, applies to one call.

### Rate limit

A set may carry `rateLimit` (see
[configuration.md](configuration.md#ratelimit)). A call over the limit is
refused as an ordinary tool error — `isError: true`, text starting `Error: ` —
and the MCP call itself stays up:

```text
Error: лимит 60 вызовов в минуту для serena/find_symbol, повторите через 980 мс
```

The delay comes from the same token bucket that refused the call. Each inner
call of `invoke_many` is counted on its own and carries the same refusal in its
`error` field, so one refused entry never hides the others. The check runs before
the set opens a downstream session: a refused call never reaches a server, and a
refusal is counted in `/stats` as `throttled`, not as a tool call.

### Profiles

A key issued with a profile (see [configuration.md](configuration.md#profiles))
is refused what the profile does not allow, in the same shape as a policy
denial — `isError: true`, `_meta.lazyMcpPolicy: "blocked"`:

```text
🚫 Blocked by mcprig policy: профиль reader не разрешает serena/find_symbol
```

The refusal is a tool result, not a broken connection: the MCP call stays up.
Inside `invoke_many` it lands in that entry's `error`, and the other entries run
as usual. `permissions` is checked first, so a command denied by a rule reports
its message rather than the profile. `list_commands` hides such commands
instead of reporting them. The profile is found by the key's fingerprint, not by
its note, and a key without a profile is not filtered at all.

`describe_commands` is not filtered: the names are hidden from the catalog, but
a schema for a guessed name is still served and the call itself is refused, so a
key learns what it is barred from — knowingly, and nothing more.

An answer longer than `maxBytes` is cut at a UTF-8 boundary; the returned text
ends with a marker naming the kept and full sizes and the file holding the full
answer:

```text
…обрезанный текст
[обрезано: 32768 из 149873 байт, полный ответ — /tmp/mcprig-data/tool-out/1f3c9a2b7d4e5f60.txt]
```

The full answer is written `0600` to
`${MCPRIG_DATA_DIR:-$TMPDIR}/tool-out/<random>.txt`; the directory is created
on first use. When the answer carried `structuredContent`, one more line follows
the marker — `[structuredContent не влезает в maxBytes: полный ответ в файле]`:
the structure does not survive the text cut, and a client whose meaning lives in
structured data has nothing to read otherwise. An answer within the cap is
returned untouched — no file, no marker. A non-numeric or negative `maxBytes`, or one above 1048576, is an
error like any other bad argument.

### Fetching the full answer over HTTP

A set served over HTTP hands the file out instead of a path: over the same
listener, under the same guard as `/mcp` — the set's bearer key, `allowedHosts`
and the `Origin` check. The marker then reads:

```text
[обрезано: 32768 из 149873 байт, полный ответ — /tool-out/1f3c9a2b7d4e5f60 (тот же ключ, что у /mcp)]
```

`GET /tool-out/<name>` returns the whole answer: `text/plain; charset=utf-8`,
`no-store`, byte ranges honoured (`Range: bytes=0-9` is a `206`), `HEAD` answering
the length without the body. The name is the file name without `.txt`, sixteen
hex characters; anything else is `404`, and so is a name whose file is gone.

A dump older than the retention TTL is `410 Gone` — the marker's promise has run
out. The endpoint reads the TTL of its own process, once at start, from
`MCPRIG_TOOLOUT_TTL` (default `24h`). A set started by the panel does not get that
variable — `MCPRIG_` is a reserved prefix there — so it keeps the default `24h`,
and a shorter panel retention may delete the file first; then the endpoint
answers `404`, not `410`.

The name is a capability, not a session: any holder of the set's key fetches any
dump whose name it knows, whoever produced it.

The endpoint is never anonymous, not even on an open set: `/mcp` may admit a
request without a key, the dump does not. A set over stdio keeps the path in the
marker — its file is on the caller's own filesystem, and there is no listener to
ask.

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
`ok: false` and `error`; the other calls are unaffected. For a permissions
refusal `result` carries the same answer `invoke_command` gives: the
`🚫 Blocked by mcprig policy: …` marker in `content` and
`_meta.lazyMcpPolicy: "blocked"`.

Bad input fails the whole request with `isError: true` and nothing is run: no
calls, more than 8, an unknown key inside a call, a missing `server` or
`command_name` (or its alias `command`), `parameters` that is not an object, or
a bad `maxBytes`.

## Resources and prompts

A downstream server's resources and prompts are off by default: the client sees
them only when the server's config names the surface in `expose`
(`"resources"`, `"prompts"`, or both — see
[configuration.md](configuration.md#expose)). An unknown value is a config
error and the server does not start, so a typo never reads as a deliberate
"expose nothing".

### list_resources / list_prompts

Input: `{"server": "notes"}` or `{}` (both fields optional; `maxBytes` is
optional too, with no default cap, as in `list_commands`).

With `server` the answer is an array; without it, an object keyed by server
name over every usable server that exposes that surface — a server without
`expose` does not appear at all:

```json
[
  {"server": "notes", "uri": "notes://welcome", "name": "welcome", "mimeType": "text/markdown", "size": 240}
]
```

```json
[
  {"server": "notes", "name": "greet", "description": "приветствие",
   "arguments": [{"name": "who", "required": true}]}
]
```

### read_resource

Input: `{"server": "notes", "uri": "notes://welcome", "maxBytes": 4096}`.
`uri` is the one `list_resources` returned. Without `maxBytes` the cap is
32768 bytes, as in `invoke_command`.

The answer carries the upstream `contents[]` unchanged — `uri`, `mimeType`, and
`text` or `blob` (base64) — cut at the cap; the marker names the file with the
whole answer, and the server's own secrets are scrubbed as in any other answer.

### get_prompt

Input: `{"server": "notes", "name": "greet", "arguments": {"who": "мир"}}`.
`arguments` is a string-to-string map; the upstream messages come back
unchanged in the `prompt` result.

### What is refused

A server without the matching `expose` entry, or a surface the server does not
have (a `resources/read` answer is `isError: true` with `Error: <server>:
набор не отдаёт ресурсы — добавьте "resources" в expose этого сервера`).

### Permissions

No new rule kind: a resource is addressed as `resource:<uri>` and a prompt as
`prompt:<name>`, matched by the usual glob rules, and the caller's profile sees
the same string:

```json
{"rules": [
  {"pattern": "resource:notes://secret/*", "action": "deny", "message": "secret"},
  {"pattern": "prompt:*", "action": "allow"}
]}
```

The gate runs before the downstream is connected, and a denial returns the same
`🚫 Blocked by mcprig policy: …` result as `invoke_command`. That applies to the
listings too: `list_resources` and `list_prompts` show only what the rules and
the caller's profile allow, so a `default: deny` yields an empty catalogue until
a rule names the resource or prompt.

## Secret scrubbing

Before an answer is counted or written to `tool-out`, its text content is
scrubbed of the server's own secrets: values of that server's `env` and
`headers` whose names contain `key`, `token`, `secret`, `pass`, `auth` or
`credential` are replaced with `***`. A leading `Bearer ` is stripped first, and
a value shorter than 8 characters is left alone. `structuredContent` is not
rewritten — that would break clients reading it. The reason: an upstream repeats
the key in an error body (tavily returns `api_key` in validation details), and
that text would otherwise reach the model context.

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
as a failure. The panel reads this through `GET /stats`; in stdio mode the same
counters and history are written to `$MCPRIG_DATA_DIR/stdio/stats/`, every call
labelled `stdio` — there is simply no panel above the process to read them.

The client label comes from `_meta["io.modelcontextprotocol/clientInfo"]` of the
request being counted — the SEP-2575 per-request client info. It is written as
`name/version` (`opencode/2.2.6`) or as the bare `name` when the client sends no
version (`mcprig`). This matters for stateless HTTP, where there is no
`initialize` handshake: the label follows every request, not the session. When
`_meta` has no client info, the label falls back to the transport: the key's
note when the key is signed (`PATCH /api/sets/<id>/keys/<keyID>`), otherwise
`key:<6 hex of a token hash>/<User-Agent product token>` for a keyed set, the
`User-Agent` alone without a key, and `unknown` when neither is present. Reading
the `/stats` of an open set without its token still redacts every label to
`(аноним)`.

## See also

- [configuration.md](configuration.md) — permissions, secrets and the servers behind the tools.
- [cli.md#init](cli.md#init) — the client fragment that connects to a set.
- [architecture.md](architecture.md) — lazy connections and the per-set process.
- [troubleshooting.md](troubleshooting.md) — when a tool call comes back as an error.

The normative contract of the rewrite era, with the shapes frozen at that time,
is in [../history/reference/meta-tools-contract.md](../history/reference/meta-tools-contract.md).
