package downstream

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

func TestTransportRejectsBrokenServer(t *testing.T) {
	cases := []struct {
		name string
		srv  *config.Server
		want string
	}{
		{"stdio without command", &config.Server{Transport: config.TransportStdio}, "пустой command"},
		{"http without url", &config.Server{Transport: config.TransportHTTP}, "пустой url"},
		{"unknown transport", &config.Server{Transport: "grpc"}, "неизвестный transport"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn := &Connector{}
			_, err := conn.transport(context.Background(), c.srv)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error %q, got %v", c.want, err)
			}
		})
	}
}

// A missing binary must surface as a connect error carrying the server name,
// not a panic and not a bare exec error.
func TestConnectWrapsFailure(t *testing.T) {
	srv := &config.Server{
		Name:      "ghost",
		Transport: config.TransportStdio,
		Command:   []string{"/nonexistent/mcp-server"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := (&Connector{Version: "test"}).Connect(ctx, srv)
	if err == nil {
		t.Fatal("want a connect error for a missing binary")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error must name the server, got %v", err)
	}
}

func TestHeadersAreApplied(t *testing.T) {
	var seen http.Header
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Clone()
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	rt := withHeaders(base, map[string]string{"X-Token": "abc"})
	req, err := http.NewRequest("GET", "https://example.test/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if got := seen.Get("X-Token"); got != "abc" {
		t.Fatalf("header not applied, got %q", got)
	}
}

// Env заменяет os.Environ целиком: в env процесса сервера не должно быть ни
// лишних переменных вызывающего, ни потери собственного env сервера.
func TestConnectorEnvHook(t *testing.T) {
	t.Setenv("MCPRIG_PASSWORD", "secret")
	srv := &config.Server{
		Name:      "env",
		Transport: config.TransportStdio,
		Command:   []string{"sh", "-c", "env"},
		Env:       map[string]string{"OWN": "1"},
	}
	c := &Connector{Env: func(*config.Server) []string {
		return []string{"PATH=/bin", "ONLY=1"}
	}}
	tr, err := c.transport(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	cmd := tr.(*mcp.CommandTransport).Command
	got := map[string]bool{}
	for _, kv := range cmd.Env {
		got[kv] = true
	}
	want := []string{"PATH=/bin", "ONLY=1", "OWN=1"}
	for _, kv := range want {
		if !got[kv] {
			t.Errorf("в env нет %q: %v", kv, cmd.Env)
		}
	}
	if len(cmd.Env) != len(want) {
		t.Errorf("env = %v, ожидались только %v", cmd.Env, want)
	}
}

// TestConnectStdioLegacyServer: сервер на mcp SDK ≤ 1.9 не отвечает на
// server/discover (падает), поэтому stdio обязан идти через initialize.
func TestConnectStdioLegacyServer(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "legacyserver")
	build := exec.Command("go", "build", "-o", bin, "./testdata/legacyserver")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка legacyserver: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := &Connector{Version: "test"}
	session, err := c.Connect(ctx, &config.Server{
		Name:      "legacy",
		Transport: config.TransportStdio,
		Command:   []string{bin},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(res.Tools) != 0 {
		t.Fatalf("инструментов %d, ждали 0", len(res.Tools))
	}
}
