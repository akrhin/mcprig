package manager

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fetchSetHealth должен донести unknown до панели и не выдумывать health
// для сервера, которого нет в ответе.
func TestFetchSetHealthCarriesUnknown(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "list_servers", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text := `{"servers":[{"name":"a","healthy":true,"unknown":true}]}`
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
		},
	)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	host, portText, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	got := fetchSetHealth(context.Background(), host, port, "", true, []string{"a", "b"}, "test")
	if len(got.Servers) != 2 {
		t.Fatalf("серверов %d, ждём 2", len(got.Servers))
	}
	a := got.Servers[0]
	if a.Healthy == nil || !*a.Healthy || !a.Unknown {
		t.Fatalf("a: ждём healthy:true, unknown:true, получили healthy=%v unknown=%v", a.Healthy, a.Unknown)
	}
	b := got.Servers[1]
	if b.Healthy != nil || b.Unknown {
		t.Fatalf("b: ждём healthy:null, unknown:false, получили healthy=%v unknown=%v", b.Healthy, b.Unknown)
	}
}
