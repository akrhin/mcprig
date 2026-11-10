# Changelog

Notable changes to mcprig, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), versions follow
[SemVer](https://semver.org/). Releases are cut from tags: Gitea is the source
of truth, GitHub (`akrhin/mcprig`) is a public mirror with the same archives.

## [Unreleased]

### Added

- Sandbox for untrusted stdio servers: `sandbox` block on a server
  (`mode: bwrap`, `onUnavailable: deny` by default), running the child under
  bubblewrap — own mount tree, read-only root, no `/home`, no network, no
  `/proc/1/root` — with `prlimit` limits inside the namespace for address space
  and process count. Fail-closed: if bubblewrap cannot start, the server does
  not start either. `scripts/sandbox-e2e.sh` checks all of it against the host.
- Servers tagged `untrusted` require a sandbox; without one the config is
  rejected and the server never runs.
- `envAllow` per server, and a warning in `mcprig doctor` for `npx -y` / `uvx`
  without a pinned version, for an `envAllow` name the environment does not
  have, and for `memoryMb` below 2048 on a node command.
- `sandbox.tmpMb` (default 128): the `/tmp` tmpfs is size-limited and carries
  `RLIMIT_FSIZE`, so a child cannot fill the host disk. `mcprig doctor` probes
  bubblewrap itself.
- `list_commands` takes `namesOnly`: an array of tool names instead of the full
  catalog (arr-status: 89 KB and 443 tools), plus the document-everywhere
  `refresh` in its schema. A multi-part text answer is now joined with newlines
  instead of being wrapped in JSON, so the cut marker points at readable text.
- tool-out is swept every 10 minutes instead of hourly: a chatty server writes
  about 2 MiB an hour, and the old cadence let the 256 MiB cap overshoot.
- `list_servers` and the panel health answer carry `isolation`: `bwrap` when
  the sandbox is applied, `без изоляции: <reason>` when it was requested but
  unavailable. The note clears once the sandbox works again.

### Fixed

- A stdio server started by `mcprig serve` or `mcprig stdio` received the whole
  environment of the host process, including every API key in it. Now it gets a
  fixed base (`PATH`, `HOME`, `LANG`, `TMPDIR`, `LC_*`, `XDG_*`, `SSL_*`,
  `TZ`, `NODE_EXTRA_CA_CERTS`) plus what `env` and `envAllow` name explicitly.
- Editing a server in the panel dropped `sandbox` and `envAllow`: the registry
  parser did not read them, so the API answered `200` and the isolation
  disappeared on the next save. Both round-trip through add, update, import and
  export now.
- `untrusted` accepted `onUnavailable: warn|allow`, which is the same
  unprotected start only quieter; only `deny` passes.
- The `probe` and `browse` buttons, and `mcprig warm`, started a server without
  the validation `config.Load` applies, and `warm` still handed the child the
  whole host environment.
- A sandbox that failed to apply was visible only in the package log; `warn`
  and `allow` made the server run unprotected while health said it was fine.
- When a child died before `initialize`, the reason in its stderr was lost:
  Go sends stderr to `/dev/null`, so `bwrap: execvp …: No such file` arrived as
  a bare `EOF`. The last 4 KB of stderr now accompanies the error, with the
  server's own secrets masked.
- A `timeoutSec` that fired left the session alive, so the next call could read
  the previous answer; the session is dropped and the error unwraps to
  `context.DeadlineExceeded`.
- `probe` hard-coded `/usr/bin/true` and ran on every connect; it now resolves
  `true` through `PATH` and caches a success.
- A missing `rw`/`cwd` path was silently skipped, leaving the server without the
  directory it was meant to have; only missing default `ro` paths are skipped.
- `Check` filled defaults in the shared registry object, racing with set
  building; it now validates a copy.

## [0.3.3] - 2026-11-10

### Added

- Panel: six colour schemes — mcprig dark (default), light, Nord, Gruvbox,
  Solarized, Tokyo Night. Chosen in the header, remembered in `localStorage`,
  the first visit follows `prefers-color-scheme`. Themes are plain classes and
  a separate `themes.css`, so the panel CSP is unchanged (no inline styles).
- `POST /api/sets` returns the issued set token once in the `201` body; lists
  and the registry view still never repeat it.

### Fixed

- Tool answers are scrubbed of server secrets before counters and `tool-out`:
  values of `env`/`headers` with key/token/secret/pass/auth/credential in the
  name are replaced with `***`, so upstream error texts no longer leak API keys.
- Panel health beats the set's own `healthTimeout`: the load budget is 30 s
  against 15 s per server, so a restarted set no longer shows
  `context deadline exceeded` for the whole set.
- `POST /api/login` answers `400` for empty fields instead of a misleading `401`.

## [0.3.2] - 2026-11-09

### Changed

- Releases are mirrored to GitHub: `.github/workflows/release.yml` builds the
  same archives (goreleaser `--skip=publish`) and uploads them with `gh`.
- The goreleaser install step in both workflows fails loudly when `checksums.txt`
  or the archive could not be downloaded, instead of dying inside `sha256sum`.

### Fixed

- The stdio handshake now uses `initialize` (protocol `2025-11-25`), not
  `server/discover`: Python MCP SDK ≤ 1.9 fails to parse the new method, stops
  reading, and the client waited out its 15 s budget — qbittorrent in the `ha`
  set showed `context deadline exceeded`. HTTP sets keep the modern handshake.

## [0.3.1] - 2026-11-09

### Added

- Docker images in the Gitea registry: `0.3.1-slim`, `0.3.1-full`, and
  `latest`/`latest-full` following the release. `--build-arg VERSION` bakes the
  version into the binary, so `/healthz` reports the release, not `dev`.

### Fixed

- `tool-out` dumps are cleaned by TTL (24 h) and a 256 MiB cap.
- `mcprig doctor` reads the panel's environment before reporting, so its port
  and secrets are seen from the panel side.

## [0.3.0] - 2026-11-09

### Added

- `invoke_many`: up to 8 independent calls in one request.
- `list_commands` can be filtered by prefix; `invoke_command` caps large answers
  (`maxBytes`) and writes the full text to `tool-out`.
- Live tool-list refresh and one safe retry per call.
- `mcprig doctor` (health report) and `mcprig warm` (cache warm-up) with a
  systemd timer.
- Panel: client label from request `_meta`, schema weight counted in tokens.

### Fixed

- `${VAR}` resolution is parallel (up to 8 at once); logins queue one scrypt at a
  time; a busy explicit port is rejected on set creation; `goreleaser check`
  runs before the release job publishes.

## [0.2.0] - 2026-11-09

### Added

- Secrets from an OpenBao-backed store with a 30 s cache; `DELETE /api/env`
  reports where the value came from.
- Hardening from the paladin/arbiter review: cross-host redirects with
  `Authorization` are refused, the login limiter counts the client address
  rather than a proxy-appended one, logs are capped at 5000 lines, `{file:…}`
  is rejected in env and headers, `store=local` accepted as an alias, a string
  `command` rejected on import, sets polled in parallel in `/api/calls`.

## [0.1.0] - 2026-11-09

### Added

- First release: one binary that puts a whole MCP set behind 5 meta-tools
  (`list_servers`, `list_commands`, `describe_commands`, `invoke_command`,
  `invoke_many`) instead of hundreds of tools in the prompt.
- Registry and web panel over the same sets: add servers, groups, keys, call
  tail, per-set logs, env and secrets.
- HTTP and stdio transports; `init` prints client configs.
- Docker images (slim and full), Gitea Actions release with goreleaser and
  govulncheck, Go 1.27.3.
