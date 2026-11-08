This file covers release archives, systemd units, Docker and the on-disk state.

# Install

## Release archives

`.goreleaser.yaml` builds one static binary per platform:

| property | value |
| --- | --- |
| OS / arch | linux, darwin × amd64, arm64 |
| archive | `mcprig_<version>_<os>_<arch>.tar.gz` |
| contents | `mcprig`, `README.md`, `LICENSE`, `deploy/mcprig-set@.service`, `deploy/mcprig.service` |
| checksum | `checksums.txt` |
| release | draft, changelog from git without `docs:` and `test:` commits |
| build | `CGO_ENABLED=0`, `-trimpath`, `-s -w -X main.version=<version>` |

The config is written but `goreleaser` is not installed on the development
host, so archives have not been produced or verified here. To build the same
binary by hand:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags)" -o mcprig ./cmd/mcprig
```

A plain `go build -o mcprig ./cmd/mcprig` prints `dev` for `mcprig -version`.

## systemd (user units)

Two units are in `deploy/`:

| unit | what it runs | config |
| --- | --- | --- |
| `mcprig.service` | `mcprig panel` — registry, supervision, panel | `~/.config/mcprig/panel.env` |
| `mcprig-set@.service` | `mcprig serve --config …` — one set as its own service | `~/.config/mcprig/sets/<set>/servers.json` and `env` |

`mcprig-set@` is an alternative to the panel: a set can run under systemd
without the manager. Sets started from the panel are child processes of the
panel, not systemd units.

The panel unit sets `MCPRIG_HOST=127.0.0.1`, `MCPRIG_PORT=9091` and
`MCPRIG_DATA_DIR=%h/.local/state/mcprig`, then reads `panel.env` for the
password or proxy secret. The set unit reads
`~/.config/mcprig/sets/%i/env` (keep it `0600`: it holds
`transport.authToken` and `${VAR}` values) and runs
`mcprig serve --config %h/.config/mcprig/sets/%i/servers.json`.

Install:

```bash
install -Dm755 mcprig ~/.local/bin/mcprig
install -Dm644 deploy/mcprig.service deploy/mcprig-set@.service ~/.config/systemd/user/
install -d -m700 ~/.config/mcprig ~/.local/state/mcprig
printf 'MCPRIG_PASSWORD=%s\n' 'change-me' > ~/.config/mcprig/panel.env
chmod 600 ~/.config/mcprig/panel.env
systemctl --user daemon-reload
systemctl --user enable --now mcprig.service
```

Both units use `Restart=on-failure`, `NoNewPrivileges=yes` and `PrivateTmp=yes`.
The panel stops with `TimeoutStopSec=15`, a set with `TimeoutStopSec=10`.
`systemd-analyze verify` reports no errors except the not-yet-installed binary
path.

## Docker

The `Dockerfile` has two targets:

| target | contents | downstreams |
| --- | --- | --- |
| `slim` (default) | binary, CA roots, non-root user `10001` | HTTP only |
| `full` | `slim` plus node and python | stdio servers launched with `npx` / `uvx` |

```bash
docker build -t mcprig:slim .
docker build --target full -t mcprig:full .
docker run --rm -p 127.0.0.1:9100:9100 \
  -v "$PWD/servers.json:/config/servers.json:ro" \
  -e MCPRIG_DATA_DIR=/data \
  mcprig:slim
```

The image entrypoint is `mcprig`; the default command is
`serve --config /config/servers.json --addr 0.0.0.0:9100`. The working
directory is `/data`, owned by uid 10001; mount a volume there and set
`MCPRIG_DATA_DIR=/data` so OAuth tokens and other state survive a restart.
`slim` ships a healthcheck against `http://127.0.0.1:9100/healthz`.

`deploy/docker-compose.yml` runs two sets (`demo` on 9100, `ops` on 9101) from
`ghcr.io/sintez/mcprig:latest`, mounts each `servers.json` read-only and keeps
state in named volumes.

The images were **not built on this host**: `docker.io` was unreachable
(`DeadlineExceeded` on `load metadata for node:22-alpine`). Build them where a
registry is available.

## State on disk

`MCPRIG_DATA_DIR` selects the state directory. Defaults:

| process | default |
| --- | --- |
| `panel` | `$XDG_STATE_HOME/mcprig`, else `~/.local/state/mcprig` |
| `serve`, `stdio` | same rule; `~/.local/state/mcprig` |

The directory is created `0700`. What lives there:

| path | contents | mode |
| --- | --- | --- |
| `registry.json` | servers, sets, keys, port allocator | `0600` |
| `registry.json.corrupt-<unix>` | a registry that failed to parse, kept aside | as written |
| `env.json` | panel-managed variables (flat strings) | `0600` |
| `manager.pid` | pid of the running panel; a second panel refuses to start | `0600` |
| `sets/<id>/servers.json` | materialized config the set process reads | `0600` |
| `sets/<id>/lazy-mcp.log` | set log; rotates to `.log.1` at 5 MiB | `0600` |
| `oauth.json` | access and refresh tokens for OAuth downstreams | `0600` |
| `oauth-clients.json` | dynamic client registration data | `0600` |

All writes go through a temp file and a rename, so a crash never leaves a
half-written file. Never copy `registry.json` while the panel is running.
