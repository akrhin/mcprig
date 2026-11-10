This file documents `servers.json`: every field the proxy reads, both command
forms, secret references, and what happens when an entry is broken.

# Configuration

A set is configured by one `servers.json`. The format is shared with lazy-mcp
and read as-is: no migration, no wrapper.

```json
{
  "servers": [
    {
      "name": "fs",
      "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/srv/docs"],
      "env": {"LOG_LEVEL": "warn"},
      "tags": ["files"]
    },
    {
      "name": "serena",
      "url": "http://127.0.0.1:8899/mcp",
      "headers": {"Authorization": "Bearer ${SERENA_TOKEN}"}
    }
  ],
  "transport": {
    "type": "http",
    "host": "127.0.0.1",
    "port": 9100,
    "path": "/mcp",
    "authToken": "{file:/home/me/.config/mcprig/demo.token}",
    "allowedHosts": ["127.0.0.1", "localhost"]
  },
  "requestTimeout": 120000
}
```

## Top level

| field | type | default | meaning |
| --- | --- | --- | --- |
| `servers` | array | required | downstream servers, see below |
| `transport` | object | required by `serve` | the listener this set exposes |
| `healthMonitor` | `{interval, timeout}` in ms | | accepted for lazy-mcp compatibility; the Go proxy does not read it |
| `requestTimeout` | int, ms | `0` (no limit) | budget for one `invoke_command`; on expiry the call returns `таймаут вызова <N> мс` |
| `permissions` | object | allow everything | global allow/deny block |
| `oauth` | object | | set-wide OAuth defaults, overridden per server |

`healthMonitor` is written by the panel (`interval: 60000`, `timeout: 120000`)
so the file stays byte-compatible with lazy-mcp, but health in mcprig is
governed by the 15 s cache described in
[architecture.md](architecture.md#health).

## Servers

| field | type | default | meaning |
| --- | --- | --- | --- |
| `name` | string | required | server name used by the meta-tools; duplicate names get `_configError: дубль имени` |
| `description` | string | empty | shown by `list_servers` and searched by `filter` |
| `transport` | `stdio` / `http` | inferred | inferred from `url` or `command` when absent |
| `command` | array or string | | stdio command; see both forms below |
| `args` | array | empty | arguments appended to a string-form `command` (legacy) |
| `cwd` | string | empty | working directory; stdio only, must be absolute; mounted read-only inside the sandbox |
| `env` | object of strings | empty | extra environment for a stdio process |
| `envAllow` | array of strings | empty | host variables passed through to a stdio process besides the fixed base (`PATH`, `HOME`, `LANG`, `TZ`, `TMPDIR`, `LC_*`, `XDG_*`, `SSL_*`, `NODE_EXTRA_CA_CERTS`); nothing else from the host environment reaches it |
| `url` | string | | HTTP endpoint; http only |
| `headers` | object of strings | empty | extra headers for every HTTP request |
| `tags` | array of strings | empty | labels for `list_servers` and `filter` |
| `sandbox` | object | absent — no isolation | bubblewrap block for a stdio server, see below. A server tagged `untrusted` must have it: without one the loader keeps it in the list as unhealthy (`_configError`) and the panel API rejects such a record with 400 |
| `enabled` | bool | `true` | disabled servers are listed but never connected |
| `permissions` | object | | per-server allow/deny block |
| `oauth` | object | | per-server OAuth settings |
| `_configError` | string | | written by the loader, never by a user |

### Sandbox

A `sandbox` block runs the stdio server under `bwrap`: its own mount tree,
read-only root, no `/home` unless `rw` or `cwd` names one, no network, no
`/proc/1/root`, and `prlimit` limits inside the namespace.

| field | type | default | meaning |
| --- | --- | --- | --- |
| `mode` | `bwrap` / `none` | `bwrap` | `none` turns isolation off for this server |
| `onUnavailable` | `deny` / `warn` / `allow` | `deny` | what happens when bubblewrap cannot start: refuse the server (fail-closed), run it and log a warning, or run it silently. An `untrusted` server accepts only `deny` |
| `ro` | array of absolute paths | `/usr /bin /lib /lib64 /etc/ssl /etc/resolv.conf /etc/passwd /etc/group` | mounted read-only; an explicit list replaces the defaults entirely, so name every directory the binary needs; only a missing *default* path is skipped, a path you named must exist |
| `rw` | array of absolute paths | empty | mounted read-write; wins over `ro` on the same path |
| `network` | `none` / `allow` | `none` | `allow` keeps the host network namespace |
| `memoryMb` | int | `2048` | `RLIMIT_AS` — virtual memory, not RSS; Go and node do not start under 512, Python fits |
| `pids` | int | `256` | `RLIMIT_NPROC`, the fork-bomb stop |
| `tmpMb` | int | `128` | size of the `/tmp` tmpfs inside the namespace, also `RLIMIT_FSIZE`; without it a server could fill the host disk |
| `timeoutSec` | int | `30` | per-tool-call deadline once the sandbox is on |

```json
{"name": "unknown", "command": ["npx", "-y", "some-mcp@1.2.3"], "tags": ["untrusted"],
 "sandbox": {"network": "none", "memoryMb": 1024}}
```

A binary outside `/usr` — node from a user prefix, for instance — must be
listed in `ro` (together with the defaults, which are replaced), or the child
cannot start. A path under `/home` gets in only through `rw` (read-write) or
`cwd` (read-only). Without `/usr` in `ro`, `prlimit` — the helper mcprig puts
the limits through — cannot start either; `doctor` warns about it beforehand.

### Both command forms

```json
{"name": "a", "command": ["npx", "-y", "some-mcp@1.2.3"]}
```

```json
{"name": "b", "command": "npx", "args": ["-y", "some-mcp@1.2.3"]}
```

Both produce the same argv. A string `command` is **not** split on spaces;
arguments come only from `args`.

### Transport inference and mistakes

| input | result |
| --- | --- |
| `url` only | `http` |
| `command` only | `stdio` |
| neither | `_configError: не удалось определить тип: ни url, ни command` |
| `stdio` with `url` | `_configError: stdio не может иметь url` |
| `http` with `command` | `_configError: http не может иметь command` |
| `cwd` on an http server | `_configError: cwd допустим только для stdio` |
| unknown `transport` | `_configError: неизвестный transport "…"` |

A server whose `name` is empty fails the whole load
(`сервер #N без имени`); every other defect is soft-failed onto that server.
Bad JSON fails the load too.

### Soft fail

A broken server keeps its `_configError`, is reported by `list_servers` as
`healthy: false` with the error text, and is excluded from `serve`'s
`servers=N` count. The rest of the set works. This is deliberate: one typo in
one entry must not stop every other server.

## Secrets

Two reference forms are expanded at load time, in `env` values, `headers`
values and `transport.authToken`:

| form | source | rules |
| --- | --- | --- |
| `${VAR}` | process environment | name must match `[A-Za-z_][A-Za-z0-9_]*`; an unset variable is an error |
| `{file:/absolute/path}` | file contents | path must be absolute; one trailing `\n` or `\r\n` is stripped |

```json
{
  "env": {"DB_PASSWORD": "{file:/run/secrets/db_password}"},
  "headers": {"Authorization": "Bearer ${SERENA_TOKEN}"}
}
```

A failed reference becomes that server's `_configError`
(`env.DB_PASSWORD: {file:relative}: нужен абсолютный путь`,
`headers.Authorization: переменная SERENA_TOKEN не задана`). References in
`command`, `url` and `cwd` are not expanded.

When the panel materializes a set, `${VAR}` stays in the file; the set process
expands it from its own environment, which includes the manager's secret
chain (OpenBao → `env.json` → process environment; `MCPRIG_*`, `BAO_*` and
`OPENBAO_*` are reserved and never handed to a downstream).

## transport

| field | default | meaning |
| --- | --- | --- |
| `type` | | accepted for compatibility; the listener is always HTTP |
| `host` | `127.0.0.1` in `serve` | bind address |
| `port` | `9100` in `serve` | bind port; the manager allocates 9100–9199 |
| `path` | `/mcp` | endpoint path; must start with `/` |
| `authToken` | | bearer token; empty refuses to start unless `allowAnonymous` |
| `allowAnonymous` | `false` | open set: no `Authorization` header is allowed |
| `allowedHosts` | loopback | `Host` and `Origin` allowlist |

`allowAnonymous` does not make a wrong token acceptable: a request that
presents `Authorization` must still match `authToken`, and an open set has no
token, so any presented token gets 401. Clients of an open set must omit the
header.

An empty `allowedHosts` means `127.0.0.1`, `localhost`, `::1`. `"*"` disables
the check and logs a warning. The same list checks `Origin` when the header is
present.

## permissions

```json
{
  "permissions": {
    "default": "allow",
    "rules": [
      {"server": "db", "pattern": "drop_*", "action": "deny", "message": "no destructive SQL"}
    ]
  }
}
```

| field | meaning |
| --- | --- |
| `default` | `allow` or `deny`; an invalid value disables the block |
| `rules[].pattern` | command name glob; `*` is any run, `?` one character, matching is case-insensitive |
| `rules[].server` | server-name glob; allowed only in the global block, default `*` |
| `rules[].action` | `allow` or `deny`; `ask` is not implemented |
| `rules[].message` | text shown when the rule denies |

Resolution order, first non-empty wins, and within a level the **last**
matching rule wins:

1. server rules;
2. global rules;
3. server `default`;
4. global `default`;
5. implicit allow.

A denial is a tool result with `isError: true`, text
`🚫 Blocked by mcprig policy: <message>` and
`_meta.lazyMcpPolicy: "blocked"`. The check runs before the downstream is
connected. An invalid global block is dropped (everything allowed); an invalid
per-server block becomes that server's `_configError`.

## oauth

The client-side authorization-code flow starts only when a server answers 401
or 403. An empty block is fine.

| field | default | meaning |
| --- | --- | --- |
| `disabled` | `false` | turn the flow off for this server |
| `callbackPort` | `8947` | loopback port for the redirect listener |
| `redirectUrl` | `http://127.0.0.1:<callbackPort>/callback` | registered redirect URI |
| `clientId` | | pre-registered client |
| `clientSecret` | | with `clientId` only; a secret without an id is an error |
| `scopes` | | requested scopes |

A server block is merged over the set-wide block; unset fields fall back to
the set-wide value, then to the default. Dynamic client registration, PKCE,
code exchange and refresh are done by the MCP SDK. Tokens are stored in
`<data dir>/oauth.json`.
