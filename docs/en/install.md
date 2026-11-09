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

Both are published to the project's Gitea registry, so you can skip the build:

```bash
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:latest        # slim
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:latest-full   # full
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:0.3.1-slim    # pinned release
docker pull frosty-ad-gt.cloudpub.ru/sintez/mcprig:0.3.1-full
```

`latest` and `latest-full` follow the newest release; the version tags pin one
pair (`0.3.1-slim`, `0.3.1-full` are the current pair).

Or build them yourself:

```bash
docker build --build-arg VERSION=v0.3.1 -t mcprig:slim .
docker build --build-arg VERSION=v0.3.1 --target full -t mcprig:full .
docker run --rm -p 127.0.0.1:9100:9100 \
  -v "$PWD/servers.json:/config/servers.json:ro" \
  -e MCPRIG_DATA_DIR=/data \
  mcprig:slim
```

The image entrypoint is `mcprig`; the default command is
`serve --config /config/servers.json`; the listen address comes from `transport.host` and `transport.port`, so mount a config whose transport says `0.0.0.0` and the port you publish. The working
directory is `/data`, owned by uid 10001; mount a volume there and set
`MCPRIG_DATA_DIR=/data` so OAuth tokens and other state survive a restart.
`slim` ships a healthcheck against `http://127.0.0.1:9100/healthz`; it assumes the
default port, so with another `transport.port` pass `--health-cmd` or set the
healthcheck in compose.

`deploy/docker-compose.yml` runs two sets (`demo` on 9100, `ops` on 9101) from
`frosty-ad-gt.cloudpub.ru/sintez/mcprig:latest`, mounts each `servers.json` read-only and keeps
state in named volumes.

Measured on this host: the bare `docker build .` gives `slim` (32 MB, binary
plus CA roots); `--target full` adds node and python and weighs 334 MB. Both
were run: `slim` served a remote `serena` over the host network and answered
`find_symbol` through `invoke_command`, and its healthcheck reports `ok`.

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
| `sets/<id>/oauth.json` | access and refresh tokens for OAuth downstreams of that set | `0600` |
| `sets/<id>/oauth-clients.json` | dynamic client registration data of that set | `0600` |
| `oauth.json` | access and refresh tokens for OAuth downstreams of a standalone `serve`/`stdio` process | `0600` |
| `oauth-clients.json` | dynamic client registration data of a standalone `serve`/`stdio` process | `0600` |

A set started by the panel gets `MCPRIG_DATA_DIR=<data>/sets/<id>`, so its
OAuth files live under `sets/<id>/`. The top-level `oauth*.json` files belong to
`serve`/`stdio` processes started with the root state directory.

All writes go through a temp file and a rename, so a crash never leaves a
half-written file. Never copy `registry.json` while the panel is running.
