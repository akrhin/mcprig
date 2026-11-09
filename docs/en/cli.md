This file lists every subcommand and flag with real examples and their output.

# CLI

`mcprig` has six subcommands. Running it with no subcommand is the same as
`mcprig serve`; running it with flags only (`mcprig -config …`) is also
`serve`.

| command | purpose |
| --- | --- |
| `serve` | one set as an HTTP MCP endpoint |
| `stdio` | one set over stdin/stdout, spawned by the client |
| `panel` | manager and web panel (registry, sets, keys, call tail) |
| `init` | print a client configuration fragment; writes nothing |
| `doctor` | one-pass health report: config, sets, keys, secrets, services |
| `warm` | load a set's tool cache so the first client call is fast |

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
empty `authToken` is a startup error unless `allowAnonymous` is true.

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

A truncated answer is saved under `${MCPRIG_DATA_DIR:-$TMPDIR}/tool-out/`. Both
`serve` and `panel` sweep those files at startup and then hourly, until the
process exits:

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
stderr. `transport` in the file is ignored. Call counters are not collected in
this mode.

## panel

```bash
MCPRIG_PASSWORD='change-me' mcprig panel
```

No flags. Everything is environment:

| variable | default | meaning |
| --- | --- | --- |
| `MCPRIG_DATA_DIR` | `$XDG_STATE_HOME/mcprig`, else `~/.local/state/mcprig` | registry, keys, logs, OAuth tokens |
| `MCPRIG_HOST` | `127.0.0.1` | panel listen address |
| `MCPRIG_PORT` | `9091` | panel port |
| `MCPRIG_CLI` | the running executable | binary spawned for each set (`serve --config …`) |
| `MCPRIG_BIND_IP` | `0.0.0.0` | `transport.host` written into every set's `servers.json` |
| `MCPRIG_SEED_FILE` | empty | JSON with `servers[]` and `sets[]`; used only when the registry is empty; new keys are generated for closed sets |
| `MCPRIG_PASSWORD` | empty | panel password |
| `MCPRIG_PASSWORD_HASH` | empty | `scrypt$N$r$p$salt$hash`; when set, the plain password is ignored |
| `MCPRIG_PROXY_SECRET` | empty | shared secret for proxy auth; a value shorter than 32 characters logs a warning |
| `MCPRIG_USER` | `admin` | login name |
| `MCPRIG_USER_HEADER` | `Remote-User` | header carrying the user name from a trusted proxy |
| `MCPRIG_ALLOWED_HOSTS` | loopback only | comma-separated `Host` allowlist |
| `MCPRIG_AUTH` | empty | `off` disables panel auth |

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
2026/11/09 00:05:27 INFO менеджер запущен addr=127.0.0.1:9080 data=/tmp/opencode/docdata auth=none
```

A second panel on the same data directory refuses to start because of
`manager.pid`.

## doctor

```bash
MCPRIG_DATA_DIR=~/.local/state/mcprig MCPRIG_PORT=9088 mcprig doctor
```

One pass over what otherwise means reading panel logs. No running panel is
required: file checks work on their own, and a dead listener is reported as a
problem rather than a crash.

| flag | default | meaning |
| --- | --- | --- |
| `-config` | `os.UserConfigDir()/mcprig/servers.json` | client configuration; when absent, servers are counted from the registry |
| `-json` | | machine-readable report with the same sections |

Sections: version and binary path; configuration (path, server count, enabled);
sets (id, port, autostart, listener, `/healthz` status); keys (count and the
active key's 8-character `keyPrefix`); `${VAR}` secrets (names only, with the
source — OpenBao, `env.json` or the environment — never values); services
(foreign listeners in `9100–9199`, `sets/*/oauth.json` size, `tool-out/` size,
panel `/healthz`).

Exit code: `0` when every line is `ok`, `1` otherwise; the last line is
`итог: N проблем`. Set listeners are dialled through `MCPRIG_BIND_IP` (a
wildcard resolves to loopback); the panel address comes from `MCPRIG_HOST` and
`MCPRIG_PORT`.

```text
mcprig doctor, версия dev (/usr/local/bin/mcprig)

[конфиг]
ok: конфиг /home/user/.config/mcprig/servers.json: 11 серверов, 11 включено

[наборы]
ok: main: порт 9101, слушает, autostart, /healthz 200

[ключи]
ok: main: 3 ключа, активный eef8bf86

[секреты]
ok: CONTEXT7_API_KEY: окружение

[служебные]
проблема: чужие порты в диапазоне 9100–9199: 9119
ok: sets/*/oauth.json нет
ok: tool-out нет
ok: панель 127.0.0.1:9088: /healthz 200

итог: 1 проблема
```

## warm

```bash
mcprig warm --set main
mcprig warm --all --panel http://127.0.0.1:9088 --password "$MCPRIG_PASSWORD"
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
main: 421 инструмент(ов), 5.6 мс
итог: 1 набор, 421 инструмент(ов), 5.6 мс
```

`-json` prints the same rows:

```json
[
  {
    "set": "main",
    "tools": 421,
    "ms": 5.6
  }
]
```

Exit code: `0` when every set warmed, `1` when any set failed (panel
unreachable, missing key, timeout); the failing line carries the reason.
`deploy/mcprig-warm.timer` repeats this every ten minutes — see
`deploy/README.md`.

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
