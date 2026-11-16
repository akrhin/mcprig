# Upgrading to 0.6

0.6.0 closes the review debts of 0.5.1, and a few of them change what a client
sees. Nothing here needs a config rewrite: a 0.5.x `servers.json` and registry
load as-is. The list below is what can produce a different answer after the
upgrade, with the fix for each.

## Before the upgrade

- Back up the registry and the sets: `cp -a ~/.local/state/mcprig ~/.local/state/mcprig.bak`
  and `cp -a ~/.config/mcprig ~/.config/mcprig.bak`.
- Run `mcprig doctor` on the new binary. A 0.5.x install may leave nothing to
  fix there — an empty installation answers `итог: 0 проблем` instead of an
  error since 0.6.0.

## What answers differently

| Symptom after the upgrade | Why | What to do |
|---|---|---|
| `405` on `GET /api/sets/<set>/servers/<server>/authorize` | Starting an OAuth flow is a state change, so it is POST only now | `POST` to start the flow and poll `GET …/authorize/status`; the panel already does this |
| `403 origin не совпадает с хостом` | The browser's `Origin` must match the panel's public name | Open the panel by the address you configured (`MCPRIG_ALLOWED_HOSTS`, or the public name your proxy sends in `X-Forwarded-Host`) |
| `403 Origin: null не принимается` | A request with a session cookie came from an unknown origin (sandboxed frame, `file://` page) | Open the panel in a normal tab. Scripts and clients without a session cookie pass with `Origin: null` as before |
| `403 браузерный запрос без Origin` | `Sec-Fetch-Site` without `Origin` is a browser-forged pair | Nothing to fix on the client: a browser always sends both |
| Forwarded headers ignored behind a proxy on another host | `X-Forwarded-*` is trusted only from loopback or from `MCPRIG_TRUSTED_PROXIES` | List the proxy address (`MCPRIG_TRUSTED_PROXIES=10.0.0.5`, or a CIDR), and make the proxy append `X-Forwarded-For` instead of passing the client's own |
| `404` on `/tool-out/<name>` | A dump belongs to the key that created it | Read the dump with the same key. Dumps made before 0.6.0 have no owner file and answer `404`; call the tool again |
| `404` on `/tool-out/<name>` after an hour | The set's `transport.toolOutTtl` (default 24h) expired the dump | Raise `transport.toolOutTtl` in the set, or re-run the call |
| `400` where 0.5.x answered `500` | Invalid registry input now names the cause instead of hiding it | Read the message: it names the field (`env.A`, `port`) |
| `409` for a busy port, a set that did not start, or a missing key | Conflicts have their own status now | Free the port, read the set log, issue a key |
| `list_servers` has no `pid` | The field exposed the proxy process | Use `runtime.pid` from `/api/state` |
| A variable shows `used: false` in the panel | It is stored but no server references it | Nothing is broken: the panel now shows stale variables instead of hiding them |
| `GET /api/calls` works for a stopped set | The call history is read from disk | — |

## Sets that children no longer survive

Since 0.6.0 a `SIGKILL` of a set process takes its stdio servers with it. A
server that used the set's death as a way to keep running on its own will not
survive; if you relied on that, run it outside the set.

## Agent prompt and panel

- `POST /api/sets/<id>/prompt` compiles tool schemas and a task into one prompt;
  secrets are masked the same way as in notes.
- The panel has pages of its own: `#/sets`, `#/servers`, `#/vars`. Old links
  (`#/stats`, `#/set/<id>`) still work.
- An empty set is a valid set: deleting the last server leaves the set without
  servers instead of refusing the deletion.

## Rolling back

0.5.1 reads the 0.6.0 registry: nothing was removed from the format. What it
cannot do is answer like 0.6.0 — a rollback is `git checkout v0.5.1` for a
source build, or the old archive from the release page.
