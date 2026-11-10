package downstream

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/oauth"
	"github.com/sintez/mcprig/internal/sandbox"
)

// Connector opens sessions to downstream servers. OAuth is optional: without a
// token store the connector behaves as before and never touches the browser.
type Connector struct {
	Version string
	OAuth   *config.OAuth // set-wide defaults, nil when the set has none
	Store   *oauth.Store  // nil when tokens cannot be persisted
	Log     *slog.Logger

	// Env задаёт окружение stdio-процесса. nil — всё окружение вызывающего:
	// так работает manage/serve. Менеджер подставляет whitelist, чтобы чужие
	// процессы не увидели его секреты.
	Env func(*config.Server) []string

	// OnSandboxFallback — песочница запрошена, но не применена: набор
	// помечает сервер в list_servers, иначе деградация невидима.
	OnSandboxFallback func(*config.Server, string)

	// OnSandboxApplied — изоляция применилась: прошлая пометка «без изоляции»
	// снимается, иначе после восстановления bwrap сервер остаётся помеченным.
	OnSandboxApplied func(*config.Server)

	// OnToolsChanged вызывается, когда сервер прислал
	// notifications/tools/list_changed. Набор по ней сбрасывает кэш
	// tools/list, и новый инструмент виден без refresh: true. SDK подписывается
	// на уведомление только если сервер объявил tools.listChanged в
	// InitializeResult (client.go:341 в go-sdk v1.8.0).
	OnToolsChanged func(*config.Server)
}

func (c *Connector) Connect(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
	tr, tail, err := c.transport(ctx, srv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", srv.Name, err)
	}
	version := c.Version
	if version == "" {
		version = "dev"
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcprig", Version: version}, c.clientOptions(srv))
	session, err := client.Connect(ctx, tr, connectOptions(srv))
	if err != nil {
		// Без хвоста stderr причина отказа теряется: Go отправляет stderr
		// ребёнка в /dev/null, и «bwrap: execvp …: No such file» приходит как EOF.
		// Секреты из env/headers маскируются: сервер может напечатать ключ.
		if tail != nil {
			if msg := config.ScrubSecrets(tail.String(), srv.Secrets()); msg != "" {
				return nil, fmt.Errorf("%s: %w (stderr: %s)", srv.Name, err, msg)
			}
		}
		return nil, fmt.Errorf("%s: %w", srv.Name, err)
	}
	return session, nil
}

// legacyProtocol — последняя ревизия до stateless server/discover (SEP-2575).
const legacyProtocol = "2025-11-25"

// connectOptions держит stdio на initialize: сервер на mcp SDK ≤ 1.9 не знает
// server/discover, не отвечает на него и падает в приёмном цикле (pydantic
// ValidationError), а initialize понимают все — потерь нет, подписка на
// tools/list_changed объявляется и в нём.
func connectOptions(srv *config.Server) *mcp.ClientSessionOptions {
	if srv.Transport != config.TransportStdio {
		return nil
	}
	return &mcp.ClientSessionOptions{ProtocolVersion: legacyProtocol}
}

// clientOptions wires the tool-list notification. The handler runs on the SDK
// read loop, so it must stay cheap: dropping a cache is all it does.
func (c *Connector) clientOptions(srv *config.Server) *mcp.ClientOptions {
	if c.OnToolsChanged == nil {
		return nil
	}
	return &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			c.OnToolsChanged(srv)
		},
	}
}

// oauthBlock merges the set-wide block under the server's own.
func (c *Connector) oauthBlock(srv *config.Server) *config.OAuth {
	cfg := &config.Config{OAuth: c.OAuth}
	return cfg.OAuthFor(srv)
}

func (c *Connector) transport(ctx context.Context, srv *config.Server) (mcp.Transport, *stderrTail, error) {
	switch srv.Transport {
	case config.TransportStdio:
		if len(srv.Command) == 0 {
			return nil, nil, fmt.Errorf("пустой command")
		}
		// Песочница — префикс argv, транспорт не меняется. Ошибка здесь значит
		// fail-closed: сервер не стартует, а не стартует «как раньше».
		res, err := sandbox.Wrap(srv, srv.Command)
		if err != nil {
			// deny-отказ тоже должен попадать в маркер изоляции: иначе list_servers обещает bwrap неработающему серверу.
			if c.OnSandboxFallback != nil {
				c.OnSandboxFallback(srv, err.Error())
			}
			return nil, nil, err
		}
		if res.Reason != "" {
			// Песочница запрошена, но не применена (onUnavailable warn|allow):
			// об этом должен знать лог набора и health, а не только пакет.
			if c.Log != nil {
				c.Log.Warn("сервер стартует без изоляции", "server", srv.Name, "причина", res.Reason)
			}
			if c.OnSandboxFallback != nil {
				c.OnSandboxFallback(srv, res.Reason)
			}
		}
		if res.Applied && c.OnSandboxApplied != nil {
			c.OnSandboxApplied(srv)
		}
		cmd := exec.Command(res.Argv[0], res.Argv[1:]...)
		cmd.Dir = srv.Cwd
		tail := &stderrTail{}
		cmd.Stderr = tail
		// A set runs with an env already narrowed by the manager; the server's
		// own env wins over it.
		base := os.Environ()
		if c.Env != nil {
			base = c.Env(srv)
		}
		cmd.Env = append(base, flatten(srv.Env)...)
		return &mcp.CommandTransport{Command: cmd}, tail, nil
	case config.TransportHTTP:
		if srv.URL == "" {
			return nil, nil, fmt.Errorf("пустой url")
		}
		tr := &mcp.StreamableClientTransport{
			Endpoint: srv.URL,
			HTTPClient: &http.Client{
				Transport:     withHeaders(http.DefaultTransport, srv.Headers),
				CheckRedirect: sameHostRedirect,
			},
		}
		handler, err := c.oauthHandler(ctx, srv)
		if err != nil {
			return nil, nil, err
		}
		// A typed-nil handler would put a non-nil OAuthHandler interface into the
		// transport, and the SDK would call TokenSource on a nil receiver.
		if handler != nil {
			tr.OAuthHandler = handler
		}
		return tr, nil, nil
	default:
		return nil, nil, fmt.Errorf("неизвестный transport %q", srv.Transport)
	}
}

// stderrTail держит последние байты stderr ребёнка: причина отказа старта
// (bwrap без бинаря, npx без пакета) приходит туда, а не в ответ протокола.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

const stderrLimit = 4096

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if n := len(t.buf) - stderrLimit; n > 0 {
		t.buf = append(t.buf[:0], t.buf[n:]...)
	}
	return len(p), nil
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// oauthHandler builds the client-side flow for one server. It binds nothing:
// the loopback listener starts only when the server actually asks for a token.
func (c *Connector) oauthHandler(ctx context.Context, srv *config.Server) (*oauth.Handler, error) {
	block := c.oauthBlock(srv)
	if block.Disabled || c.Store == nil {
		return nil, nil
	}
	h, err := oauth.NewHandler(ctx, oauth.Options{
		ServerName:   srv.Name,
		ServerURL:    srv.URL,
		RedirectURL:  block.RedirectURL,
		CallbackAddr: fmt.Sprintf("127.0.0.1:%d", block.CallbackPort),
		ClientID:     block.ClientID,
		ClientSecret: block.ClientSecret,
		Scopes:       block.Scopes,
		Store:        c.Store,
		Log:          c.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("oauth: %w", err)
	}
	return h, nil
}

// sameHostRedirect: заголовки сервера (в том числе Authorization) ставятся на
// каждый RoundTrip, поэтому кросс-хостовый редирект унёс бы их чужому хосту —
// встроенная защита Go здесь не работает.
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("слишком много редиректов")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("редирект на другой хост отклонён: %s", req.URL.Host)
	}
	return nil
}

func flatten(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func withHeaders(base http.RoundTripper, headers map[string]string) http.RoundTripper {
	if len(headers) == 0 {
		return base
	}
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return base.RoundTrip(r)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
