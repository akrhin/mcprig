package httplisten

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
)

// The key fingerprint must reach the tool handler on the context even when the
// key has a note: the note replaces the display label, and a rate limit that
// bucketed by the label would give two keys one bucket (or let one caller mint
// quotas by changing its client info). This walks the real HTTP handler.
func TestClientKeyReachesHandler(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test-set", Version: "0"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "who", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "key:" + stats.ClientKey(ctx)}}}, nil
		},
	)
	cfg := &config.Config{Transport: &config.HTTPTransport{
		AuthToken: testToken,
		// Подпись ключа: метка для /stats станет этой строкой, отпечатка в ней
		// нет — и всё равно инструмент обязан увидеть отпечаток.
		ClientNames: map[string]string{stats.Fingerprint(testToken): "named-caller"},
	}}
	h, err := New(cfg, srv, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientInfo":{"name":"test","version":"0"},` +
		`"io.modelcontextprotocol/clientCapabilities":{}},` +
		`"name":"who","arguments":{}}}`
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "who")
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	raw := drain(t, res)
	want := "key:" + stats.Fingerprint(testToken)
	if !strings.Contains(raw, want) {
		t.Fatalf("инструмент не увидел отпечаток ключа (%s): %q", want, raw)
	}
}

// An open set has no key, so the caller's address must reach the handler: it is
// the only thing a rate limit can bucket such a caller by.
func TestClientAddrReachesHandler(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test-set", Version: "0"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "who", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "addr:" + stats.ClientAddr(ctx)}}}, nil
		},
	)
	h, err := New(&config.Config{Transport: &config.HTTPTransport{AllowAnonymous: true}}, srv, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientInfo":{"name":"test","version":"0"},` +
		`"io.modelcontextprotocol/clientCapabilities":{}},` +
		`"name":"who","arguments":{}}}`
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "who")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	raw := drain(t, res)
	if !strings.Contains(raw, `addr:127.0.0.1`) {
		t.Fatalf("инструмент не увидел адрес клиента: %q", raw)
	}
}
