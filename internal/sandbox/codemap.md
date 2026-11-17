# internal/sandbox/

## Responsibility

Wraps a stdio server's `argv` in bubblewrap: private mount tree, read-only root,
no `/home`, no network by default; transport unchanged, so `mcp.CommandTransport`
works (`sandbox.go:1-4`). `Probe` says whether the sandbox is usable (`:295`).

## Design

- **Availability.** `probe` (`:43-71`) runs once per process — success cached,
  failure not, concurrent callers wait on `done` (`:35-41`). `probeRun` (`:73-117`)
  needs `bwrap`, `prlimit`, `true` in PATH, resolves `true` through symlinks
  (`:90`), then runs `bwrap --unshare-all --die-with-parent --ro-bind-try /usr
  /bin /lib /lib64 -- <true>` under a 10 s `probeTimeout` (`:30`, `:98-103`);
  `execvp` in stderr = exec failed, else namespaces refused (`:110-114`).
- **Modes.** `mode: none` or no block → `argv` untouched; `mode: bwrap` →
  prefixed. On probe failure `onUnavailable` decides: `warn`/`allow` return
  `Result{Argv, Reason}`, `deny` (default) errors and the server does not start
  (`Wrap`, `:132-157`).
- **Mounts** (`bwrapArgv`, `:159-274`). Paths are `filepath.Clean`ed, then must
  pass `safepath.Path` — absolute, no `..`, no control characters (`:171-176`) —
  and each stays one argv element, so a flag-looking value cannot become a flag
  (`:162-166`). Missing is fatal except defaults from `config.DefaultSandboxRO`
  (`:177-185`, `:206-210`);
  duplicates merge, `--bind` beats `--ro-bind` (`:181-187`). Base mounts first
  so a config path under `/tmp` is not hidden: `--size` = `tmpMb`, `--tmpfs /tmp`
  `--proc /proc --dev /dev` (`:196-199`); `srv.Cwd` bound read-only (`:212-216`);
  mounts sorted by `/` count, parent before child (`:237-240`).
- **Kernel policy.** `--remount-ro /` makes a write to `/` an error, not a silent
  write to the private tmpfs (`:244-246`); `--unshare-user --unshare-pid
  --unshare-ipc --unshare-uts --unshare-cgroup-try`, `--unshare-net` unless
  `network: allow` (`:247-251`), `--die-with-parent --new-session`, `--chdir`,
  `--setenv TMPDIR /tmp` (`:252-259`).
- **Limits via prlimit, inside the namespace.** `limitArgs` (`:278-291`): `--as`
  (bytes only, util-linux takes no `M` here), `--nproc`, `--fsize` (= `tmpMb`).
  `prlimit` is dynamic, so it and `/lib`, `/lib64`, `/usr/lib`, `/usr/lib64` are
  bound in (`:221-236`) and exec'd by absolute path inside the userns (`:261-269`):
  `RLIMIT_NPROC` counts the uid in the namespace.

## Flow

`Wrap(srv, argv)` → nil/`none`: `argv` as is; empty: `sandbox: пустая команда`;
probe error → `onUnavailable` → `Reason` or `sandbox unavailable: …`; else
`bwrapArgv` → `{Argv: wrapped, Applied: true}`. `Result{Argv, Applied, Reason}`
(`:123-127`): `Applied=false` **with** `Reason` = requested, not applied.

## Integration

- `internal/downstream/connect.go:126` calls `Wrap` in `transport()` for
  `TransportStdio`: error is fail-closed (`:127-133`, via `OnSandboxFallback`);
  non-empty `Reason` logs "сервер стартует без изоляции" and takes the same
  fallback (`:135-144`); `Applied` → `OnSandboxApplied` (`:145-147`).
- `cmd/mcprig/doctor.go:25` uses `Probe`.
- Config: `config.Sandbox` (`internal/config/config.go:55`), `DefaultSandboxTmpMb`
  = 128 (`:88`), `DefaultSandboxRO = /usr /bin /lib /lib64 /etc/ssl`
  `/etc/resolv.conf /etc/passwd /etc/group` (`:93`); `untrusted` requires
  `mode: bwrap` + `onUnavailable: deny` (`:715-722`).
- External: `bwrap` (bubblewrap), `prlimit` (util-linux), `true` (coreutils).
