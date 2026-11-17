[← Docs index](index.md)
EN · [RU](../ru/cli.md)

This file lists every subcommand and flag with real examples and their output.

# CLI

`mcprig` has ten subcommands. Running it with no subcommand is the same as
`mcprig serve`; running it with flags only (`mcprig -config …`) is also
`serve`.

| command | purpose |
| --- | --- |
| `serve` | one set as an HTTP MCP endpoint |
| `stdio` | one set over stdin/stdout, spawned by the client |
| `auth` | authorize a server that asks for OAuth; prints the link, saves the token |
| `panel` | manager and web panel (registry, sets, keys, call tail) |
| `init` | print a client configuration fragment; writes nothing |
| `doctor` | one-pass health report: config, sets, keys, secrets, services, sandbox |
| `warm` | load a set's tool cache so the first client call is fast |
| `approve` | approve a tool descriptor that changed under quarantine |
| `status` | panel health and set state in the terminal |
| `logs` | tail of a set's or a server's log, with `-f` |

Exit codes: `0` after help, `2` for a command-line error (unknown subcommand,
extra positional argument, unknown client), `1` for a runtime error (config
missing, port busy, refused to start without auth) and for a `doctor` or
`warm` report with at least one problem.

## serve

```bash
mcprig serve --config ~/.config/mcprig/sets/demo/servers.json
```

| flag | default | meaning |
| --- | --- | --- |
| `-config` | `os.UserConfigDir()/mcprig/servers.json` (Linux: `~/.config/mcprig/servers.json`) | set configuration |
| `-addr` | from `transport.host` and `transport.port`, else `127.0.0.1:9100` | listen address; overrides the config |
| `-version` | | print the build version and exit |

`-addr` changes only the listener; `transport.path` (default `/mcp`),
`authToken`, `allowAnonymous` and `allowedHosts` still come from the file. An
empty `authToken` is a startup error unless `allowAnonymous` is true. A set the
panel created keeps its file in the state directory —
`$MCPRIG_DATA_DIR/sets/<id>/servers.json` — while
`~/.config/mcprig/sets/<set>/` is where the `mcprig-set@` systemd unit keeps
its own ([install.md](install.md#state-on-disk)).

Output goes to stderr:

```text
time=2026-11-09T00:10:09.939+03:00 level=INFO msg="прокси слушает" addr=127.0.0.1:9184 servers=2
```

`servers` counts usable entries: enabled and with no configuration error.

The listener comes up before any downstream is touched, and every enabled
server is warmed in the background right after it: the first client call after
that warm-up returns in milliseconds instead of waiting for eleven `npx`/`uvx`
servers to unpack. `MCPRIG_PPROF=127.0.0.1:6060` exposes `net/http/pprof` on
that address for any subcommand; unset by default, so nothing is exposed.

### tool-out retention

A truncated answer is saved under `${MCPRIG_DATA_DIR:-$TMPDIR}/tool-out/`.
`serve`, `stdio` and `panel` sweep that one directory at startup and then every
ten minutes, until the process exits — stdio uses the base directory, not its
own:

| variable | default | meaning |
| --- | --- | --- |
| `MCPRIG_TOOLOUT_TTL` | `24h` | files older than this are removed; `time.ParseDuration` syntax |
| `MCPRIG_TOOLOUT_MAX` | `256MiB` | size cap of a tool-out directory; oldest files go first, `B`/`K`/`KiB`/`M`/`MiB`/`G`/`GiB` accepted |

A file younger than five minutes is never evicted by size — a client may still
be reading the path from the truncation marker. A value that does not parse logs
a warning and the default is used. `mcprig doctor` shows the file count, the
size and the age of the oldest file.

## stdio

```bash
mcprig stdio --config ~/.config/mcprig/sets/demo/servers.json
```

| flag | default | meaning |
| --- | --- | --- |
| `-config` | same default as `serve` | set configuration |

There is no listener and no key: the client owns the process, and the
transport is the boundary. The proxy writes MCP messages to stdout and logs to
stderr. `transport` in the file is ignored. Calls are counted and written to
history like a set's own: `$MCPRIG_DATA_DIR/stdio/stats/` holds the same
`raw-YYYY-MM-DD.jsonl`, `day-*.json` and `month-*.json` files, so the period
breakdown can be read back; the client is always labelled `stdio`, since the
transport is the only client the process has. If the directory cannot be
opened, a warning goes to stderr and the proxy keeps serving. Downstream stdio
servers are opened with the `initialize` handshake
(protocol `2025-11-25`), which every server understands; HTTP downstreams keep
the modern `server/discover` handshake.

## auth

```bash
mcprig auth --set main swaggerhub
```

Prints the authorization link and waits for the redirect; it never starts a
browser, so the command works over SSH and from a machine without a graphical
session. The token is written to the set's `oauth.json` at `<data dir>/sets/<id>/oauth.json`,
where the set process reads it on its next start — restart the set in the panel
afterwards.

| flag | default | meaning |
| --- | --- | --- |
| `-set` | | set name: config and token store come from its directory |
| `-config` | | `servers.json` instead of `-set`; the token then lands in the data dir of the calling process |
| `-timeout` | `6m` | how long the link may stay unused |

The positional argument is the server name. A server that needs no OAuth
connects and exits with a line saying so.

The panel does the same from a browser: **authorize** in the server row, the
link is drawn in a dialog and the code comes back to the panel address — see
[panel.md](panel.md#oauth). The CLI keeps a loopback receiver, which is what a
provider demanding `https` for any other address still accepts.

The interactive flow does not run inside a client request on purpose: a set's
health probe allows 15 s, while a person at a browser needs minutes. Without a
token the set reports the server unhealthy; `auth` is the way past it.

## panel

```bash
MCPRIG_PASSWORD='change-me' mcprig panel
```

`-h` / `--help` prints the twenty-seven environment variables the panel reads,
one per line and in the same order as below, and exits `0`; any other argument is
a usage error and exits `2`. The panel takes no flags: everything is environment —
the twenty-four rows in the table, plus `MCPRIG_TOOLOUT_TTL` and
`MCPRIG_TOOLOUT_MAX` ([tool-out retention](#tool-out-retention)) and
`MCPRIG_PPROF` ([serve](#serve)).

| variable | default | meaning |
| --- | --- | --- |
| `MCPRIG_DATA_DIR` | `$XDG_STATE_HOME/mcprig`, else `~/.local/state/mcprig` | registry, keys, logs, OAuth tokens |
| `XDG_STATE_HOME` | `~/.local/state` | base of the state directory; `MCPRIG_DATA_DIR` overrides it |
| `MCPRIG_CLI` | the running executable | binary spawned for each set (`serve --config …`) |
| `MCPRIG_SEED_FILE` | empty | JSON with `servers[]` and `sets[]`; used only when the registry is empty; new keys are generated for closed sets |
| `MCPRIG_HOST` | `127.0.0.1` | panel listen address |
| `MCPRIG_PORT` | `9091` | panel port |
| `MCPRIG_PUBLIC_URL` | empty | public address of the panel, `http(s)://host[:port][/prefix]`; when set it is the only source of the OAuth redirect and of the `Secure` cookie decision. Validated at startup: scheme, host, no query/fragment, no trailing `/` |
| `MCPRIG_BIND_IP` | `0.0.0.0` | `transport.host` written into every set's `servers.json` |
| `MCPRIG_PASSWORD` | empty | panel password |
| `MCPRIG_PASSWORD_HASH` | empty | `scrypt$N$r$p$salt$hash`; when set, the plain password is ignored |
| `MCPRIG_USER` | `admin` | login name |
| `MCPRIG_AUTH` | empty | `off` disables panel auth |
| `MCPRIG_TLS_CERT` | empty | certificate for the panel listener; set together with `MCPRIG_TLS_KEY` — one without the other refuses to start, and an unreadable file fails startup |
| `MCPRIG_TLS_KEY` | empty | private key for the panel listener |
| `MCPRIG_PROXY_SECRET` | empty | shared secret for proxy auth; a value shorter than 32 characters logs a warning |
| `MCPRIG_TRUSTED_PROXIES` | loopback | comma-separated IPs or CIDRs whose `X-Forwarded-*` are believed; an entry that is neither stops the panel at startup |
| `MCPRIG_USER_HEADER` | `Remote-User` | header carrying the user name from a trusted proxy |
| `MCPRIG_ALLOWED_HOSTS` | loopback only | comma-separated `Host` allowlist |
| `BAO_ADDR` | empty | OpenBao address for set secrets; empty leaves secrets in the local env file only |
| `OPENBAO_ADDR` | empty | same as `BAO_ADDR`, used when that one is empty |
| `BAO_TOKEN` | empty | OpenBao token; without it and `BAO_ADDR` secrets stay local |
| `OPENBAO_TOKEN` | empty | same as `BAO_TOKEN`, used when that one is empty |
| `BAO_MOUNT` | `secret` | KV v2 mount inside OpenBao |
| `BAO_PREFIX` | `mcp` | path prefix inside that mount |

Setting `MCPRIG_PROXY_SECRET` is what makes `X-Forwarded-*` trusted, and only
from a peer the panel believes: loopback, or an address in
`MCPRIG_TRUSTED_PROXIES`. A request from any other peer is treated as if the
headers were absent — no public name, no `https`, no client address for the login
limit. Trusting a proxy on another host therefore takes both the secret and its
address, and a mismatch shows up as `403` on the origin check with the proxy
serving the panel correctly. List the proxy alone (a `/32` where you can) and
keep the panel port unreachable for everyone else, and make the proxy append or
overwrite `X-Forwarded-For`: anyone who reaches the panel from a trusted address
controls those headers.
With an `http` panel behind the secret, browser writes are rejected because the
request counts as `https`; set `MCPRIG_PUBLIC_URL=https://…` to match.

Auth modes are derived from what is set:

| password | proxy secret | `MCPRIG_AUTH` | mode |
| --- | --- | --- | --- |
| any | any | `off` | none |
| yes | no | | password |
| no | yes | | proxy |
| yes | yes | | proxy + password |

With none of them the panel refuses to start:
`API открыт: задайте MCPRIG_PASSWORD или MCPRIG_AUTH=off`.

`MCPRIG_BIND_IP=0.0.0.0` is the default for the **set** listeners, not for the
panel. Sets are protected by their bearer keys; the panel itself still binds
loopback unless `MCPRIG_HOST` says otherwise.

```text
2026/11/09 00:05:27 WARN менеджер: MCPRIG_ALLOWED_HOSTS пуст — принимаются только loopback-хосты
2026/11/09 00:05:27 WARN панель без пароля (MCPRIG_AUTH=off): доступна всем, кто дотянется до порта; задайте MCPRIG_PASSWORD
2026/11/09 00:05:27 INFO менеджер запущен addr=127.0.0.1:9080 scheme=http publicUrl= data=/var/lib/mcprig auth=none
```

`MCPRIG_PUBLIC_URL` is the address the panel is reached at from outside. It is
the only source of the OAuth redirect (`<publicUrl>/oauth/callback`, the path
prefix kept) and decides that the session cookie is `Secure`; without it the
panel keeps building the redirect from `MCPRIG_HOST` and `MCPRIG_PORT`. A value
that does not parse, has no host, carries a query or fragment, or ends with `/`
stops the panel at startup.

With `MCPRIG_TLS_CERT` and `MCPRIG_TLS_KEY` the panel serves https itself; the
session cookie is `Secure` whenever the public address is `https` or the panel
has its own certificate, even though a TLS reverse proxy hands the process plain
http. `X-Forwarded-Proto` and `X-Forwarded-Host` are trusted only when
`MCPRIG_PROXY_SECRET` is set **and** the peer is loopback or listed in
`MCPRIG_TRUSTED_PROXIES` — otherwise they are ignored: they neither mark a
request `https` nor name the panel for the Host and Origin checks, and the OAuth
redirect never depends on them.

A second panel on the same data directory refuses to start because of
`manager.pid`.

## doctor

```bash
MCPRIG_DATA_DIR=~/.local/state/mcprig MCPRIG_PORT=9091 mcprig doctor
```

One pass over what otherwise means reading panel logs. No running panel is
required: file checks work on their own, and a dead listener is reported as a
problem rather than a crash. The panel password is read from
`~/.config/mcprig/panel.env` first and from the older `env` file second; quotes,
`export` and comments are understood.

| flag | default | meaning |
| --- | --- | --- |
| `-config` | `os.UserConfigDir()/mcprig/servers.json` | client configuration; when absent, servers are counted from the registry |
| `-json` | | machine-readable report with the same sections |

Five sections: configuration (path, server count, enabled); sets (id, port,
autostart, listener, `/healthz` status); keys (count and the active key's
8-character `keyPrefix`); `${VAR}` secrets (names only, with the source —
OpenBao, `env.json` or the environment — never values); services (foreign
listeners in `9100–9199`, `sets/*/oauth.json` size, `tool-out/` size, panel
`/healthz`, and the effective OAuth redirect). The version and binary path line is printed above the first
heading. The sandbox is a line inside the services section: it runs `bwrap`
with a user namespace and reports `песочница: bwrap запускается, userns
разрешён`, or a problem when a config actually asks for isolation and bwrap or
userns is missing — exit code 1.

Exit code: `0` when every line is `ok`, `1` otherwise; the last line is
`итог: N проблем`. Set listeners are dialled through `MCPRIG_BIND_IP` (a
wildcard resolves to loopback); the panel address comes from `MCPRIG_HOST` and
`MCPRIG_PORT`. The same section prints the effective OAuth redirect built from
`MCPRIG_PUBLIC_URL` (or the panel address): no host, port `0` or a loopback host
behind `https` is a problem, because an identity provider cannot send the code
there.

An install that has not been made yet is not a fault: when neither the client
config nor the registry exists, `doctor` prints one line — `установка пуста:
конфиг … и реестр … не созданы, запустите mcprig init` — then `итог: 0 проблем`
and exit code `0`. A registry that is there but does not parse is still a
problem (exit `1`). Under `-json` the empty sections are `[]`, not `null`, so a
consumer does not have to tell "nothing yet" from "not read".

With `MCPRIG_AUTH=off` and neither `MCPRIG_PASSWORD` nor `MCPRIG_PASSWORD_HASH`
set, the services section adds a warning line — `предупреждение: панель без пароля …`
— because an open panel is reachable by anyone who gets to the port; it counts
as a warning, not a problem, so the exit code stays `0`.

```text
mcprig doctor, версия dev (/usr/local/bin/mcprig)

[конфиг]
ok: конфиг /home/user/.config/mcprig/servers.json: 11 серверов, 11 включено

[наборы]
ok: example: порт 9101, слушает, autostart, /healthz 200

[ключи]
ok: example: 3 ключа, активный eef8bf86

[секреты]
ok: CONTEXT7_API_KEY: окружение

[служебные]
ok: чужие порты в диапазоне 9100–9199: 9119 (выдача портов их пропускает)
ok: sets/*/oauth.json нет
ok: tool-out нет
ok: панель 127.0.0.1:9091: /healthz 200
ok: redirect OAuth: http://127.0.0.1:9091/oauth/callback

итог: 0 проблем
```

## warm

```bash
mcprig warm --set example
mcprig warm --all --panel http://127.0.0.1:9091 --password "$MCPRIG_PASSWORD"
mcprig warm --config ~/.config/mcprig/sets/demo/servers.json
```

`warm` loads the set's tool cache: `list_servers`, then one `list_commands`
without `refresh` (that flag would clear the cache instead). A client call right
after returns in milliseconds instead of waiting for every downstream to
unpack. The path is the client's: an HTTP MCP request to the set, or, with
`-config`, `internal/mcpserver` in-process with no network at all.

| flag | default | meaning |
| --- | --- | --- |
| `-url` | empty: the set endpoint comes from the panel `/api/sets` | direct set endpoint (`http://127.0.0.1:9101/mcp`), no panel needed |
| `-panel` | `MCPRIG_PANEL_URL`, else `http://127.0.0.1:$MCPRIG_PORT` | panel address: the set's port for `-set`, every set for `-all` |
| `-set` | `main` | set name: token file and report line |
| `-all` | | every set listed by the panel; one line per set |
| `-timeout` | `2m` | budget for one set |
| `-token` | empty | bearer key (visible in `ps`; prefer `-token-file`) |
| `-token-file` | empty | file with the bearer key |
| `-config` | empty | local `servers.json`: in-process warm, no network |
| `-json` | | the same lines as an array of `{set, tools, ms, error}` |
| `-password` | `MCPRIG_PASSWORD` | panel password for `-all` and for resolving a set's port by name |
| `-secret` | `MCPRIG_PROXY_SECRET` | panel proxy secret for `-all` |
| `-user` | `admin` | panel login name |

Key precedence: `-token`, `-token-file`, `MCPRIG_TOKEN`, then
`~/.config/mcprig/<set>.token`; with none of them the command fails and points
to `mcprig init`. An open set is warmed without a key.

```text
example: 421 инструмент, 5.6 мс
итог: 1 набор, 421 инструмент, 5.6 мс
```

`-json` prints the same rows:

```json
[
  {
    "set": "example",
    "tools": 421,
    "ms": 5.6
  }
]
```

Exit code: `0` when every set warmed, `1` when any set failed (panel
unreachable, missing key, timeout); the failing line carries the reason.
`deploy/mcprig-warm.timer` repeats this every ten minutes — see
`deploy/README.md`.

## approve

```bash
mcprig approve --set main serena/find_symbol
mcprig approve --set main serena/find_symbol gitea/create_pull_request
mcprig approve --set main --all
```

A tool whose descriptor changed after it was approved is quarantined: the proxy
refuses its calls until a person approves the new descriptor
([meta-tools.md](meta-tools.md#descriptor-quarantine)). The refusal names this
command. The first appearance of a tool is approved automatically, so `approve`
only ever deals with a genuine change.

The panel owns the record: the command reads and writes it through the panel
API, the same two routes the **Tools** drawer uses. `--all` takes every tool
that is currently changed; without it each argument is one `server/tool`.

| flag | default | meaning |
| --- | --- | --- |
| `-set` | `main` | set name |
| `-all` | | approve every changed tool of the set |
| `-panel` | `MCPRIG_PANEL_URL`, else `http://127.0.0.1:$MCPRIG_PORT` | panel address |
| `-password` | `MCPRIG_PASSWORD` | panel password |
| `-secret` | `MCPRIG_PROXY_SECRET` | panel proxy secret |
| `-user` | `admin` | panel login name |

```text
набор main: одобрен serena/find_symbol
```

`--all` lists what it approved:

```text
набор main: одобрено 2
  gitea/create_pull_request
  serena/replace_symbol_body
```

A tool that did not change is refused by the panel — `нечего одобрять:
serena/find_symbol не менялся или неизвестен` — and the command exits `1`.

The panel writes the record with `approvedBy: "panel"`: the drawer and this
command are one client of one API, and the CLI has no identity of its own.
Exit code: `0` when everything asked for was approved — `--all` with nothing
changed is also `0`; `1` when the panel refused a tool (unchanged or unknown)
or the request failed; `2` on a command-line error (both `--all` and a list, or
neither).

## status

```bash
mcprig status
mcprig status --panel http://127.0.0.1:9091 --json
mcprig status --set example
```

One shot of what the panel shows: `/healthz` (public, so a dead panel is an
error naming the address rather than a stack trace) and `/api/state` (every set
with its port, state, key and runtime error). The tool count and the last call
time come from `/api/calls`: `0` and `—` mean the set is stopped or that request
did not answer — it is best-effort and never fails the command.

| flag | default | meaning |
| --- | --- | --- |
| `-panel` | `MCPRIG_PANEL_URL`, else `http://127.0.0.1:$MCPRIG_PORT` | panel address |
| `-set` | empty: every set | one set, by id |
| `-json` | | `{panel, health, sets: [{id, name, state, port, tools, key, lastCall, error}]}` |
| `-timeout` | `10s` | budget for the whole report |
| `-password` | `MCPRIG_PASSWORD` | panel password (kept out of the output) |
| `-secret` | `MCPRIG_PROXY_SECRET` | reverse-proxy secret |
| `-user` | `admin` | panel login name |

```text
mcprig status, панель http://127.0.0.1:9091
здоровье: ok

набор    состояние   порт  инструментов  ключ  последний вызов
example  запущен     9101  12            есть  2026-11-15 13:00:11
lab      остановлен  9102  —             нет   —

итог: 2 набора
```

Exit code: `0` when the panel answered; `1` otherwise (unreachable, `401` — add
`-password` or `-secret`, unknown `-set`).

## logs

```bash
mcprig logs --set example
mcprig logs --set example --server context7 -n 50
mcprig logs --set example -f --json
```

`logs` reads `/api/sets/<id>/logs`, or `/api/sets/<id>/servers/<name>/logs` with
`-server`, passing the same `lines` parameter the panel uses. With `-f` it polls
once a second and prints only lines it has not printed yet; Ctrl-C exits with
code `0`. A repeating log is the one exception: when the whole answer is a tail
of the previous one and is not identical to it, `logs` prints the answer again
instead of staying silent — the panel tail carries no cursor.
`-json` prints one object per line: `{"set","server","line","ts"}` when
the panel returns structured lines, `{"line":"…"}` while it returns plain
strings.

| flag | default | meaning |
| --- | --- | --- |
| `-panel` | `MCPRIG_PANEL_URL`, else `http://127.0.0.1:$MCPRIG_PORT` | panel address |
| `-set` | required | set whose log is read |
| `-server` | empty: the whole set | one server's log |
| `-n` | `200` | how many last lines to take |
| `-f` | | follow: poll once a second, print only new lines |
| `-json` | | one object per line |
| `-password` | `MCPRIG_PASSWORD` | panel password (kept out of the output) |
| `-secret` | `MCPRIG_PROXY_SECRET` | reverse-proxy secret |
| `-user` | `admin` | panel login name |

Exit code: `0` at EOF and on Ctrl-C, `1` when the panel refuses (unknown set or
server, `401`, unreachable).

## init

`init` prints a fragment for the client's own configuration file. It never
edits that file.

```bash
mcprig init --client opencode --set demo --config ~/.config/mcprig/sets/demo/servers.json
```

| flag | default | meaning |
| --- | --- | --- |
| `-client` | `opencode` | one of `opencode`, `claude`, `codex` |
| `-set` | `demo` | set name used in the generated server name (`mcprig-<set>`) |
| `-remote` | empty | set URL, e.g. `http://127.0.0.1:9100/mcp`; empty means stdio |
| `-key` | empty | bearer token for `--remote`; `-` reads stdin |
| `-key-file` | empty | file holding the bearer token; preferred over `-key`, whose value is visible in `ps` and shell history |
| `-binary` | `mcprig` | binary path for stdio mode |
| `-config` | empty | `servers.json` path for stdio mode (required) |

Key precedence: `-key-file`, then `-key` (`-` means stdin), then `MCPRIG_KEY`.

### stdio output

```bash
mcprig init --client opencode --set demo --config /tmp/demo/servers.json
```

```json
{
  "mcp": {
    "servers": {
      "mcprig-demo": {
        "command": [
          "mcprig",
          "stdio",
          "--config",
          "/tmp/demo/servers.json"
        ],
        "enabled": true,
        "type": "local"
      }
    }
  }
}
```

```bash
mcprig init --client claude --set demo --config /tmp/demo/servers.json
```

```json
{
  "mcpServers": {
    "mcprig-demo": {
      "args": [
        "stdio",
        "--config",
        "/tmp/demo/servers.json"
      ],
      "command": "mcprig"
    }
  }
}
```

```bash
mcprig init --client codex --set demo --config /tmp/demo/servers.json
```

```toml
[mcp_servers.mcprig-demo]
command = "mcprig"
args = ["stdio", "--config", "/tmp/demo/servers.json"]
```

### remote output

```bash
mcprig init --client opencode --set demo --remote http://127.0.0.1:9100/mcp --key-file ./demo.key
```

```json
{
  "mcp": {
    "servers": {
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
}
```

For `claude` the root object is `mcpServers` and `type` is `http`; for `codex`
the fragment is a `[mcp_servers.<name>]` table with `url` and
`http_headers = { Authorization = "…" }`.

### errors

| input | exit | message |
| --- | --- | --- |
| `--key` without `--remote` | 2 | `--key и --key-file нужны только с --remote: в stdio-режиме ключ не требуется` |
| `--remote` without a key | 2 | `для --remote нужен ключ: --key, --key-file, --key - или MCPRIG_KEY` |
| stdio without `--config` | 2 | `для stdio-режима нужен --config (путь к servers.json набора)` |
| unknown client | 2 | `неизвестный клиент "nope"; доступны: opencode, claude, codex` |
| key file missing | 1 | `ключ: open /nope: no such file or directory` |

CLI help, diagnostics and log lines are Russian in this build; the document
translates their meaning, not the strings.

## See also

- [install.md](install.md) — the systemd units that run `panel` and `serve`.
- [configuration.md](configuration.md) — the file `-config` points at.
- [panel.md](panel.md) — the UI `mcprig panel` serves, and its access modes.
- [troubleshooting.md](troubleshooting.md) — reading `mcprig doctor` output.
