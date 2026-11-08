package downstream

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcp-manager/internal/config"
)

// Connect opens one session to a downstream server. The caller owns Close.
func Connect(ctx context.Context, srv *config.Server, version string) (*mcp.ClientSession, error) {
	tr, err := transport(srv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", srv.Name, err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-manager", Version: version}, nil)
	session, err := client.Connect(ctx, tr, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", srv.Name, err)
	}
	return session, nil
}

func transport(srv *config.Server) (mcp.Transport, error) {
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
		// No client timeout: a streamable session holds the connection open.
		return &mcp.StreamableClientTransport{
			Endpoint:   srv.URL,
			HTTPClient: &http.Client{Transport: withHeaders(http.DefaultTransport, srv.Headers)},
		}, nil
	default:
		return nil, fmt.Errorf("неизвестный transport %q", srv.Transport)
	}
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
