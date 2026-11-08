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
	"github.com/sintez/mcp-manager/internal/config"
)

const testToken = "s3cret-token"

func testServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test-set", Version: "0"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)
	return srv
}

func newHandler(t *testing.T, token string, allowed []string) (http.Handler, *bytes.Buffer) {
	t.Helper()
	cfg := &config.Config{Transport: &config.HTTPTransport{AuthToken: token, AllowedHosts: allowed}}
	var buf bytes.Buffer
	h, err := New(cfg, testServer(), slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h, &buf
}

// initializeBody is a legacy (pre-2026-07-28) initialize: no per-request
// `_meta` triple, which is what the stateless handler synthesizes a session
// from. The modern path is exercised in TestNewProtocolToolsList.
const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

// postMCP sends an initialize POST to /mcp through srv.
func postMCP(t *testing.T, srv *httptest.Server, token, host, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(initializeBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return res
}

func drain(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}

func TestAuth(t *testing.T) {
	h, _ := newHandler(t, testToken, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	res := postMCP(t, srv, "", "", "")
	drain(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("без токена: статус %d, ожидался 401", res.StatusCode)
	}
	if got := res.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, ожидался Bearer", got)
	}

	res = postMCP(t, srv, "wrong", "", "")
	drain(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("неверный токен: статус %d, ожидался 401", res.StatusCode)
	}
}

func TestAuthorizedInitialize(t *testing.T) {
	h, logs := newHandler(t, testToken, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	res := postMCP(t, srv, testToken, "", "")
	body := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("initialize: статус %d, тело %q", res.StatusCode, body)
	}
	for _, want := range []string{"protocolVersion", "test-set"} {
		if !strings.Contains(body, want) {
			t.Errorf("в ответе нет %q: %q", want, body)
		}
	}
	if strings.Contains(logs.String(), testToken) {
		t.Errorf("токен попал в лог: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "status=200") {
		t.Errorf("в логе нет строки запроса: %s", logs.String())
	}
}

func TestNewProtocolToolsList(t *testing.T) {
	h, _ := newHandler(t, testToken, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// SEP-2575: a 2026-07-28 call carries the version triple in `_meta` and
	// matches it in the MCP-Protocol-Version header.
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientInfo":{"name":"test","version":"0"},` +
		`"io.modelcontextprotocol/clientCapabilities":{}}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/list")
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	raw := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("tools/list 2026-07-28: статус %d, тело %q", res.StatusCode, raw)
	}
	if !strings.Contains(raw, `"ping"`) {
		t.Errorf("в списке нет тестового тула: %q", raw)
	}
}

func TestHostAllowlist(t *testing.T) {
	h, _ := newHandler(t, testToken, []string{"mcp.local"})
	srv := httptest.NewServer(h)
	defer srv.Close()

	res := postMCP(t, srv, testToken, "", "")
	drain(t, res)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой Host: статус %d, ожидался 403", res.StatusCode)
	}

	res = postMCP(t, srv, testToken, "mcp.local", "")
	body := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("свой Host: статус %d, тело %q", res.StatusCode, body)
	}
}

func TestOrigin(t *testing.T) {
	h, _ := newHandler(t, testToken, []string{"mcp.local"})
	srv := httptest.NewServer(h)
	defer srv.Close()

	res := postMCP(t, srv, testToken, "mcp.local", "http://evil.example")
	drain(t, res)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой Origin: статус %d, ожидался 403", res.StatusCode)
	}

	res = postMCP(t, srv, testToken, "mcp.local", "http://mcp.local:9100")
	drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("свой Origin: статус %d, ожидался 200", res.StatusCode)
	}
}

func TestBodyTooLarge(t *testing.T) {
	h, _ := newHandler(t, testToken, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(bytes.Repeat([]byte("a"), 5<<20)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	drain(t, res)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("тело 5 МиБ: статус %d, ожидался 413", res.StatusCode)
	}
}

func TestHealthzIsPublic(t *testing.T) {
	h, _ := newHandler(t, testToken, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body := drain(t, res)
	if res.StatusCode != http.StatusOK || body != "ok" {
		t.Fatalf("/healthz: статус %d, тело %q", res.StatusCode, body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h, _ := newHandler(t, testToken, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/mcp", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	drain(t, res)
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("PUT: статус %d, ожидался 405", res.StatusCode)
	}
	if got := res.Header.Get("Allow"); got == "" {
		t.Error("405 без заголовка Allow")
	}
}

func TestEmptyTokenRefusesToStart(t *testing.T) {
	cfg := &config.Config{Transport: &config.HTTPTransport{}}
	if _, err := New(cfg, testServer(), nil); err == nil {
		t.Fatal("пустой authToken принят, ожидалась ошибка")
	}
}
