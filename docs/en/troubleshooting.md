[← Docs index](index.md)
EN · [RU](../ru/troubleshooting.md)

# Troubleshooting

Ten failures that really happened, in the shape symptom → cause → what to do.
Nothing here is hypothetical; each entry ends in the page that has the details.

## 1. The set does not start: the port is taken

**Symptom.** The panel shows the set as stopped, or `POST …/start` answers
`порт 9100 занят другим процессом` / `порт 9100 занят набором other`.

**Cause.** Ports 9100–9199 are shared by sets, the test suites and other services
on the host; the allocator skips ports a foreign listener already holds, but an
explicit `port` in the config is checked and refused.

**What to do.** Let the allocator pick a port, or take a free one.
`mcprig doctor` lists foreign listeners in that range — they are skipped on
purpose. Tests and sets must never run in parallel in one range: `go test …
-p 1`.

## 2. One server is dead, the rest of the set works

**Symptom.** `list_servers` returns `healthy: false` with an error text, and
`serve` starts with `servers=N` one lower.

**Cause.** A broken entry is soft-failed on purpose so one typo never bricks the
set. Typical texts: `не удалось определить тип: ни url, ни command`,
`stdio не может иметь url`, `cwd допустим только для stdio`,
`неизвестные поля: …`.

**What to do.** Fix that one entry — the text names the field. A missing server
`name` or invalid JSON is the exception: it fails the whole load.

## 3. `context deadline exceeded` on a server built with the Python SDK

**Symptom.** A stdio server times out during `initialize` while the same command
answers by hand in under a second.

**Cause.** go-sdk opens a session with `server/discover` (SEP-2575); a server on
Python `mcp` 1.9.0 has no such method, rejects the request and dies without
answering — the proxy only sees its own 15-second deadline. Fixed in v0.3.2:
stdio sessions go through `initialize` with revision `2025-11-25`, HTTP still
uses `server/discover`.

**What to do.** Upgrade. On an older build there is no setting that helps.

## 4. The client connects but sees no tools

**Symptom.** The client's tool list is empty, or every request comes back `401`.

**Cause.** A set is closed by default: it wants a bearer key, and
`allowAnonymous: false` is the default. The reverse mistake also bites: an
*open* set still rejects any `Authorization` header it is given, because it has
no token to compare against.

**What to do.** Take the fragment `mcprig init --client … --set <id>` prints and
the key that sits next to it; for an open set, remove the header rather than
invent one.

## 5. `invoke_command` answers «лишние ключи»

**Symptom.** A call fails with an unknown-tool or extra-keys error although the
tool exists.

**Cause.** The tool name goes into `command_name`. `command` is not a field of
`invoke_command` — it belongs to `servers.json`.

**What to do.** `{"server": "…", "command_name": "…", "parameters": {…}}`.
`describe_commands` shows the parameter schema.

## 6. OAuth: the callback port is busy

**Symptom.** `oauth: занять порт 127.0.0.1:8947 для приёма кода: … (занят?
смените callbackPort в настройках)`.

**Cause.** The loopback receiver takes 8947 by default, and two sets
authenticating at the same moment, or anything else on that port, collide.

**What to do.** Give the set its own `oauth.callbackPort`. Tokens are stored
under the set's data directory and survive a restart, so the browser is needed
once, not on every start.

## 7. The sandbox refuses to start a server

**Symptom.** `sandbox unavailable: …`, or the server fails with `sandbox: путь
"…" не существует`, and inside a container bwrap prints
`Can't mount proc on /proc: Operation not permitted`.

**Cause.** bubblewrap needs unprivileged user namespaces. Docker's default
seccomp profile denies them; a rootless daemon denies more than seccomp does.
The default `onUnavailable: deny` is fail-closed on purpose — and a server
tagged `untrusted` accepts nothing else.

**What to do.** Start the container with `--security-opt seccomp=unconfined`;
on a rootless daemon only `--privileged` was measured to work. For a server you
do not tag `untrusted`, `onUnavailable: warn` runs it unconfined with a warning
in the set log.

## 8. `npx` or `uvx` is missing in the container

**Symptom.** `npx: executable file not found in $PATH`, or a server dies right
after start; also `prlimit не найден в PATH`.

**Cause.** The `slim` image carries the binary, CA roots, `bwrap` and `prlimit`
— no node, no uv. `prlimit` must exist in `PATH` because the sandbox mounts it
by absolute path.

**What to do.** Use the `full` image for `npx`/`uvx` servers. Both images ship
`util-linux`; on a build of your own, install it.

## 9. The panel answers 403, or is unreachable from another machine

**Symptom.** Writes fail with `403 "нужен заголовок Origin"`; after a reverse
proxy an `https` Origin is rejected; or the panel refuses connections from the
LAN.

**Cause.** Three separate rules: a cookie-authenticated write must carry
`Origin` (browsers send it, scripts often do not); `Origin` is compared in full
— scheme, host and port — so behind TLS the request looks like plain http
unless the proxy says otherwise; and `MCPRIG_HOST` unset means loopback only.

**What to do.** Send `Origin`; set `X-Forwarded-Proto: https` (and
`X-Forwarded-Host`, plus `X-Forwarded-Port` when the public port differs) in the
proxy; bind `MCPRIG_HOST` and list the host in `MCPRIG_ALLOWED_HOSTS` — the list
names the panel itself, not the sources allowed to call it. There is no
built-in TLS: keep the panel behind a proxy.

## 10. The answer came back cut

**Symptom.** The result ends with `[обрезано: 32768 из 149873 байт, полный
ответ — /…/tool-out/1f3c9a2b7d4e5f60.txt]`.

**Cause.** `maxBytes` defaults to 32768 (`invoke_command` and `invoke_many`;
`list_commands` and `describe_commands` cut nothing without it), so a large
answer is cut and the full one is written to `tool-out/`.

**What to do.** Read the file named in the marker (it lives 24 hours, the
directory is capped at 256 MiB), or pass a larger `maxBytes` — up to 1048576.

## See also

- [cli.md#doctor](cli.md#doctor) — one pass over config, sets, keys, secrets, ports.
- [configuration.md](configuration.md) — the field behind each message.
- [install.md](install.md) — Docker images, sandbox prerequisites, state on disk.
- [glossary.md](glossary.md) — set, meta-tool, tool-out, sandbox.
