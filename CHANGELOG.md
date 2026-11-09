# Changelog

Notable changes to mcprig, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), versions follow
[SemVer](https://semver.org/). Releases are cut from tags: Gitea is the source
of truth, GitHub (`akrhin/mcprig`) is a public mirror with the same archives.

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
