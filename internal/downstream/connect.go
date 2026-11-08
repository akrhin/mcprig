package downstream

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/oauth"
)

// Connector opens sessions to downstream servers. OAuth is optional: without a
// token store the connector behaves as before and never touches the browser.
type Connector struct {
	Version string
	OAuth   *config.OAuth // set-wide defaults, nil when the set has none
	Store   *oauth.Store  // nil when tokens cannot be persisted
	Log     *slog.Logger
}

// Connect opens one session to a downstream server. The caller owns Close.
func Connect(ctx context.Context, srv *config.Server, version string) (*mcp.ClientSession, error) {
	c := &Connector{Version: version}
	return c.Connect(ctx, srv)
}

func (c *Connector) Connect(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
	tr, err := c.transport(ctx, srv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", srv.Name, err)
	}
	version := c.Version
	if version == "" {
		version = "dev"
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcprig", Version: version}, nil)
	session, err := client.Connect(ctx, tr, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", srv.Name, err)
	}
	return session, nil
}

// oauthBlock merges the set-wide block under the server's own.
func (c *Connector) oauthBlock(srv *config.Server) *config.OAuth {
	cfg := &config.Config{OAuth: c.OAuth}
	return cfg.OAuthFor(srv)
}

func (c *Connector) transport(ctx context.Context, srv *config.Server) (mcp.Transport, error) {
	switch srv.Transport {
	case config.TransportStdio:
		if len(srv.Command) == 0 {
			return nil, fmt.Errorf("пустой command")
		}
		cmd := exec.Command(srv.Command[0], srv.Command[1:]...)
		cmd.Dir = srv.Cwd
		// A set runs with an env already narrowed by the manager; the server's
		// own env wins over it.
		cmd.Env = append(os.Environ(), flatten(srv.Env)...)
		return &mcp.CommandTransport{Command: cmd}, nil
	case config.TransportHTTP:
		if srv.URL == "" {
			return nil, fmt.Errorf("пустой url")
		}
		tr := &mcp.StreamableClientTransport{
			Endpoint:   srv.URL,
			HTTPClient: &http.Client{Transport: withHeaders(http.DefaultTransport, srv.Headers)},
		}
		handler, err := c.oauthHandler(ctx, srv)
		if err != nil {
			return nil, err
		}
		tr.OAuthHandler = handler
		return tr, nil
	default:
		return nil, fmt.Errorf("неизвестный transport %q", srv.Transport)
	}
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
