package downstream

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

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
	_, err := Connect(ctx, srv, "test")
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
