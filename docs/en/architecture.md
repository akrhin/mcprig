This file explains how the processes fit together, why each set is its own
process, and where health, statistics and OAuth live.

# Architecture

## Layers

```text
MCP client ──HTTP/stdio──▶ set process (mcprig serve) ──stdio/HTTP──▶ downstream MCP servers
                                  ▲
                                  │ spawn, config, /stats
                            mcprig panel
```

| layer | responsibility |
| --- | --- |
| panel | registry, key issuing, supervision, secrets, HTTP API and UI |
| set process | the five meta-tools, downstream sessions, health, counters, OAuth |
| downstream | a stdio child process or an HTTP MCP endpoint |

The panel is a control plane: it never proxies MCP traffic. Clients talk to
the set process directly, at `http://<host>:<port>/mcp` with the set's bearer
token (or over stdio when the client spawns `mcprig stdio`).

## One process per set

`mcprig panel` starts a set by spawning
`<cli> serve --config <dataDir>/sets/<id>/servers.json` with its own process
group, `MCPRIG_DATA_DIR` and `MCPRIG_SET=<id>`. Consequences:

- a crash or restart of one set does not affect another;
- each set has its own port (9100–9199), bearer key and log file;
- a set can be run alone under systemd, without the panel;
- a downstream stdio process is a grandchild of the panel but dies with its
  set's process group.

The panel materializes `servers.json` atomically (`0600`) before every start,
so the file on disk is always what the running set was given. Start checks
that the port is free, spawns the process, then waits up to 15 s for the port
to accept a TCP connection.

Restart policy:

| event | behaviour |
| --- | --- |
| exit code 0 | treated as a normal exit, no restart |
| exit code ≠ 0 | restart after `1s << n`, capped at 30 s, at most 5 times |
| port never opens | process killed, `набор … не поднялся: порт … не слушает` |
| stop | `SIGTERM` to the process group, then `SIGKILL` after 5 s |

Logs are appended to `sets/<id>/lazy-mcp.log`, rotate to `.log.1` at 5 MiB, and
the last 2000 lines are kept in memory for `GET /api/sets/<id>/logs`.

## Lazy connections

Nothing is connected at startup. The first tool call that needs a server opens
its session; later calls reuse it. A concurrent first call shares the in-flight
connect (single-flight per server), and a failed attempt is retried on the next
call, not cached forever.

If the downstream closes the session (`ErrConnectionClosed`), `invoke_command`
resets the session, reconnects once and repeats the call. A second failure is
returned with its reason.

## Health

`list_servers` probes servers by opening a session, with:

| parameter | value |
| --- | --- |
| cache TTL | 15 s, failures included |
| timeout per probe | 15 s |
| concurrency | at most 8 probes at once |

A disabled server is not probed and reports `healthy: true`: disabled is not
the same as broken. A server with `_configError` reports `healthy: false` and
the error text without any connection attempt.

The panel's own health view (`GET /api/sets/<id>/servers`) polls the set's
`list_servers` and caches the answer for 15 s with a single-flight guard, so
opening the dashboard does not multiply probes across browser tabs.

## Statistics

Counters live in the set process (`internal/stats`): per client, server and
tool, with call/failure counts, total milliseconds, p50/p95 from a 256-sample
reservoir, and a ring of the last 512 calls. Client labels come from the MCP
`clientInfo` in stdio mode and from the `User-Agent` product token plus a
6-character fingerprint of the presented key in HTTP mode. Labels and tool
names are capped at 64 bytes; the number of counters is capped at 4096, the
rest collapses into `(прочее)`; error text in the ring is capped at 512 bytes.

The panel does not keep counters: `GET /api/calls` polls `GET /stats` of every
running set with a 2 s timeout and merges the answers. A set that does not
answer is reported only by id. Tokens are not counted, because MCP responses
carry no standard usage field.

## HTTP transport

Each set serves one stateless endpoint:

- `2026-07-28` per request, with the same handler answering older protocol
  versions;
- `GET`, `POST`, `DELETE` on `transport.path` (default `/mcp`);
- `Host` and `Origin` checked against `allowedHosts`;
- bearer token compared in constant time;
- 4 MiB request body cap;
- no read timeout (an SSE stream must not be cut), 60 s idle timeout;
- `GET /healthz` public, `GET /stats` protected by the same bearer.

## OAuth

The client-side flow runs inside the set process and only when a downstream
answers 401 or 403. It is an authorization-code flow with PKCE; dynamic client
registration, code exchange and refresh are handled by the MCP SDK
(`golang.org/x/oauth2` underneath).

| item | value |
| --- | --- |
| redirect listener | `127.0.0.1:<callbackPort>`, default 8947 |
| redirect URI | `http://127.0.0.1:<port>/callback`, overridable |
| default port | fixed on purpose: some authorization servers only accept a hand-registered URI |
| token storage | `<data dir>/oauth.json` (`0600`), client registrations in `oauth-clients.json` |
| token key | first 32 hex of `sha256(serverName + "\x00" + serverURL)` |

The listener binds nothing until a token is actually needed. Tokens survive a
restart, so a refresh does not require the browser again.
