# internal/oauth/

## Responsibility

Client side of OAuth 2.0 for MCP servers that answer 401/403: a token store, a
one-shot loopback receiver for the authorization code, and a factory that builds
an `auth.AuthorizationCodeHandler` for the go-sdk transport. It supplies what the
SDK does not persist — client registration, tokens across restarts, the redirect
endpoint.

- `store.go:30` `Store` — `oauth.json` (tokens) and `oauth-clients.json` (client
  registrations) under one directory.
- `callback.go:28` `Callback` — loopback receiver, bound only during a login.
- `handler.go:22` `Handler` embeds `*auth.AuthorizationCodeHandler`; `NewHandler`
  (`handler.go:68`) wires options into the SDK config.

## Design

**The SDK does the protocol; this package does the state.** Discovery, dynamic
client registration, PKCE, code exchange and refresh belong to
`auth.AuthorizationCodeHandler` (`handler.go:65-67`). `NewHandler` only derives
the listener address, chooses pre-registered vs dynamic client, filters scopes,
seeds the token source from disk and installs the persistence hook.

**Nothing binds until a token is needed.** The handler is built eagerly but opens
no socket; `NewCallback` runs inside the code fetcher (`handler.go:103`), only
after the server demanded authorization, so a healthy HTTP set has no open port.

**Redirect address comes from `redirectUrl`, never from a guess.** `callbackAddr`
(`handler.go:53`) parses the redirect URI and refuses a missing or `:0` port
(`:61-63`): the browser returns to exactly that port, so binding another
guarantees a five-minute timeout. `Callback` also refuses non-loopback hosts
(`callback.go:59`, `isLoopbackHost:181`), defaulting to `127.0.0.1:8947`
(`callback.go:15,49`).

**`state` is mandatory and checked before `?error`.** `NewCallback` rejects an
empty state (`callback.go:46-48`); `handleCallback` compares state first
(`:135-142`), so a foreign redirect — including one carrying `?error=` — cannot
cancel the owner's pending login; then it reports `error`/`error_description`
(`:144-152`) or a missing `code` (`:154-159`) before serving the static success
page (`:161-164`, `successPage:189`). `Result` is `{Code, State, Iss}` (`:21`).

**One login at a time.** `Callback.Wait` rejects a second concurrent waiter
(`callback.go:96-99`); `Options.SingleAttempt` does the same process-wide via a
`CompareAndSwap` (`handler.go:89-93`), because a second link silently invalidates
the one the user already opened. `Wait` adds a 5-minute cap (`callbackTimeout`,
`callback.go:18,110-114`) only when the caller brought no deadline.

**Tokens are keyed by server identity, not index.** `Key` (`store.go:24`) is the
first 32 hex chars of `sha256(name + "\x00" + url)`, so renaming a set keeps its
tokens. `Save`/`SaveClient` write the whole map atomically (`WriteNoSync`,
`store.go:171`) and roll back in-memory state on error (`:122-129`, `:151-158`).
No fsync on purpose: the cache is recoverable by re-authorizing, and fsync on a
loaded disk blows the Connect deadline around `NewTokenSource` (`:162-165`).

**Refresh without a browser needs the registration.** `savedTokenSource`
(`handler.go:187`) rebuilds an `oauth2.Config` from `ClientInfo{ClientID,
ClientSecret, TokenURL, AuthStyle}` (`store.go:41`); without it the token is
served statically and an expired token just yields the normal 401 flow. Both
paths wrap the source in `persistingSource` (`handler.go:210`), which writes to
disk only on a changed access token (`:225-231`) under a background context.

**Token never reaches the caller.** This package sets no `Authorization` header;
the SDK's auth handler goes to the transport as `OAuthHandler`
(`internal/downstream/connect.go:197-199`) and attaches the bearer token above
the `sameHostRedirect` guard. The upstream client sees tools and results only;
error text is scrubbed by `config.ScrubSecrets` (`connect.go:76`).

**Browser is injectable two ways.** `Options.Browser` replaces `BrowserOpen`
(`handler.go:251`, `openBrowser:254` — `xdg-open`/`open`, `Start` without
waiting); its failure is a warning, not an error, since the URL is logged
(`:108-112`). `Options.Authorize` instead receives the URL and returns a
`Result` — how the panel accepts the redirect on its own public address.

## Flow

Authorization (first connect to a protected server):
1. `oauthHandler` (`internal/downstream/connect.go:233`) returns nil when
   `block.Disabled` or no store.
2. `NewHandler` (`handler.go:68`): validate `RedirectURL`/`ServerName`, derive
   `addr` (`:83`) and `key` (`:87`), load a saved token and, with a known
   registration, install `savedTokenSource` as `InitialTokenSource` (`:148-154`).
3. Transport connects, gets 401/403; the SDK starts the authorization-code flow
   and calls the fetcher (`handler.go:90`) with the authorization URL.
4. `SingleAttempt` CAS (`:91`) → log the URL (`:94`) → `Authorize(ctx, url)`
   (`:96-102`) or `NewCallback(addr, stateFromURL(url))` (`:103`) → `Browser` →
   `cb.Wait(ctx)` (`:113`), whose `defer cb.Close()` frees the port.
5. Browser hits `GET /callback`; state check, then success page and `Result` on
   the buffered channel (`callback.go:167-172`).
6. SDK exchanges the code; `NewTokenSource` (`handler.go:156`) saves the token
   and `ClientInfo` (`:161-172`) and returns `persistingSource`.

Refresh (later connects): `savedTokenSource` returns `persistingSource` over an
`oauth2.Config` built from `ClientInfo` (`handler.go:192-205`); `Token()` saves a
changed `AccessToken` and logs a failed write as a warning (`:220-236`).

## Integration

Consumers:
- `internal/downstream/connect.go:16,238` — builds the handler per HTTP server
  with merged `config.OAuth` fields, store, logger, browser, `SingleAuthorization`.
- `internal/manager/oauth.go:101` — panel flow: `OpenStore(DataDir/sets/<id>)`;
  `waitAuthorization:149` + `handleAuthCallback:189` are the `Authorize` side;
  `CallbackURL:234` pins `<publicUrl>/oauth/callback`.
- `cmd/mcprig/auth.go:93,117` — CLI: `OpenStore(setDir)` then `Connect` with
  `SingleAuthorization: true`; success is read back via
  `store.Load(oauth.Key(name, url))` (`:126`).
- `cmd/mcprig/main.go:258,508`, `cmd/mcprig/warm.go:184` — `OpenStore(stateDir())`
  for `serve`, `run` and `warm`.

Dependencies: `github.com/modelcontextprotocol/go-sdk/auth` and `.../oauthex`,
`golang.org/x/oauth2`, `internal/atomicfile` (write + rename), `internal/config`
(`OAuth` block, `OAuthFor`, `DefaultCallbackPort = 8947`).

Disk in scope: `<dir>/oauth.json` (tokens, `map[key]*oauth2.Token`, 0600),
`<dir>/oauth-clients.json` (`map[key]ClientInfo`), directory 0700
(`store.go:51-77`). Real `<dir>`: `stateDir()` = `$MCPRIG_DATA_DIR`,
`$XDG_STATE_HOME/mcprig` or `~/.local/state/mcprig` (`cmd/mcprig/main.go:564-576`);
`<data dir>/sets/<id>` for a set's panel login and for `mcprig auth -set`.
