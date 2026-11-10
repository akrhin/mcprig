package httplisten

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	h, err := New(cfg, testServer(), slog.New(slog.NewTextHandler(&buf, nil)), nil)
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
	if _, err := New(cfg, testServer(), nil, nil); err == nil {
		t.Fatal("пустой authToken принят, ожидалась ошибка")
	}
}

// allowAnonymous lets the set run with no token at all: a request without an
// Authorization header passes, but a presented token must still be right.
func TestAnonymousAllowed(t *testing.T) {
	cfg := &config.Config{Transport: &config.HTTPTransport{AllowAnonymous: true}}
	h, err := New(cfg, testServer(), nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	res := postMCP(t, srv, "", "", "")
	body := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("анонимный запрос: статус %d, тело %q", res.StatusCode, body)
	}

	res = postMCP(t, srv, "wrong", "", "")
	drain(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("неверный токен при allowAnonymous: статус %d, ожидался 401", res.StatusCode)
	}
}

// An empty allowedHosts is loopback only: the SDK rebinding guard is off, so
// this list is the whole Host policy.
func TestEmptyAllowedHostsIsLoopback(t *testing.T) {
	h, _ := newHandler(t, testToken, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	res := postMCP(t, srv, testToken, "", "")
	body := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("loopback Host: статус %d, тело %q", res.StatusCode, body)
	}

	res = postMCP(t, srv, testToken, "evil.example", "")
	drain(t, res)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой Host при пустом списке: статус %d, ожидался 403", res.StatusCode)
	}
}

// "*" is the explicit escape hatch and is announced in the log.
func TestWildcardAllowedHosts(t *testing.T) {
	h, logs := newHandler(t, testToken, []string{"*"})
	srv := httptest.NewServer(h)
	defer srv.Close()

	res := postMCP(t, srv, testToken, "evil.example", "http://evil.example")
	drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Host/Origin при \"*\": статус %d, ожидался 200", res.StatusCode)
	}
	if !strings.Contains(logs.String(), "rebinding") {
		t.Errorf("нет предупреждения о \"*\": %s", logs.String())
	}
}

// The label the counters see: User-Agent product token plus a key
// fingerprint, without the key itself.
func TestClientLabel(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("User-Agent", "opencode/2.2.6 (linux)")
	req.Header.Set("Authorization", "Bearer secret-token")

	got := clientLabel(req, nil)
	if !strings.HasPrefix(got, "key:") || !strings.HasSuffix(got, "/opencode/2.2.6") {
		t.Fatalf("label = %q", got)
	}
	if strings.Contains(got, "secret-token") {
		t.Fatalf("label leaks the key: %q", got)
	}
	if open := clientLabel(httptest.NewRequest("POST", "/mcp", nil), nil); open != "" {
		t.Fatalf("anonymous label = %q, want empty", open)
	}
}

// Отпечаток — ровно 6 hex, и только от реально предъявленного bearer-ключа:
// пустое значение, чужая схема и заголовок "Bearer " без ключа не дают метки.
func TestClientLabelFingerprintShape(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("User-Agent", "harness/1")
	req.Header.Set("Authorization", "Bearer "+testToken)
	got := clientLabel(req, nil)
	head, _, ok := strings.Cut(got, "/")
	if !ok {
		t.Fatalf("label = %q", got)
	}
	fp := strings.TrimPrefix(head, "key:")
	if len(fp) != 6 {
		t.Fatalf("отпечаток %q длиной %d, ожидалось 6 hex", fp, len(fp))
	}
	if _, err := hex.DecodeString(fp); err != nil {
		t.Fatalf("отпечаток %q не hex: %v", fp, err)
	}

	for _, header := range []string{"", "Bearer ", "Bearer   ", "Basic dXNlcjpwd2Q="} {
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.Header.Set("User-Agent", "harness/1")
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got := clientLabel(r, nil); strings.Contains(got, "key:") {
			t.Fatalf("заголовок %q дал отпечаток: %q", header, got)
		}
	}
}

// Подпись ключа важнее отпечатка и User-Agent: владелец уже назвал клиента,
// и в счётчике видно имя, а не «key:ce80c4/python-httpx2/2.15.1». Без имени
// остаётся прежняя метка.
func TestClientLabelUsesKeyNote(t *testing.T) {
	names := map[string]string{stats.Fingerprint(testToken): "hermes"}
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("User-Agent", "python-httpx/0.28.1")
	req.Header.Set("Authorization", "Bearer "+testToken)

	if got := clientLabel(req, names); got != "hermes" {
		t.Fatalf("label = %q, want hermes", got)
	}

	// Имя другого ключа не подходит: метка остаётся отпечатком с User-Agent.
	other := map[string]string{stats.Fingerprint("чужой-ключ"): "hermes"}
	got := clientLabel(req, other)
	if !strings.HasPrefix(got, "key:") || !strings.HasSuffix(got, "/python-httpx/0.28.1") {
		t.Fatalf("label = %q, want key:<fp>/python-httpx/0.28.1", got)
	}
	if strings.Contains(got, "hermes") {
		t.Fatalf("чужое имя попало в метку: %q", got)
	}
}

// Bearer с лишними пробелами после схемы — тот же токен: метка клиента и
// проверка доступа обязаны читать заголовок одинаково, иначе запрос получает
// отпечаток в счётчике и 401 в ответ.
func TestBearerTokenTrimsSpace(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("User-Agent", "harness/1")
	req.Header.Set("Authorization", "Bearer  "+testToken)
	tok, ok := bearerToken(req)
	if !ok || tok != testToken {
		t.Fatalf("bearerToken = %q, %v", tok, ok)
	}
	if !bearerOK(req, testToken) {
		t.Fatal("bearerOK отбил токен с двойным пробелом")
	}
	if got := clientLabel(req, nil); got != "key:"+stats.Fingerprint(testToken)+"/harness/1" {
		t.Fatalf("label = %q", got)
	}
}

func newStatsHandler(t *testing.T, token string, allowAnonymous bool, rec *stats.Recorder) http.Handler {
	t.Helper()
	cfg := &config.Config{Transport: &config.HTTPTransport{
		AuthToken:      token,
		AllowAnonymous: allowAnonymous,
	}}
	h, err := New(cfg, testServer(), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), rec)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

// Открытый набор: /stats анонимно отдаётся, но с редакцией — без меток
// клиентов и текстов ошибок апстрима, иначе первый же прохожий читает чужие
// ошибки. Счётчики и хвост при этом на месте.
func TestOpenSetStatsRedacted(t *testing.T) {
	rec := stats.NewRecorder("open", 4)
	rec.Record("opencode/2.2.6", "fix", "echo", 5*time.Millisecond, errors.New("upstream boom"))
	srv := httptest.NewServer(newStatsHandler(t, "", true, rec))
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/stats")
	if err != nil {
		t.Fatalf("get /stats: %v", err)
	}
	raw := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/stats открытого набора: статус %d, тело %q", res.StatusCode, raw)
	}
	for _, want := range []string{stats.AnonymousLabel, `"echo"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("/stats без ключа: нет %q в теле %q", want, raw)
		}
	}
	for _, forbidden := range []string{"upstream boom", "opencode/2.2.6"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("/stats без ключа отдал %q: %q", forbidden, raw)
		}
	}
}

// Закрытый набор: /stats требует тот же bearer, что и /mcp, и не отдаёт ключ.
func TestStatsNeedsTokenAndNeverLeaksIt(t *testing.T) {
	rec := stats.NewRecorder("closed", 4)
	rec.Record("opencode/2.2.6", "fix", "echo", 5*time.Millisecond, errors.New("upstream boom"))
	srv := httptest.NewServer(newStatsHandler(t, testToken, false, rec))
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/stats")
	if err != nil {
		t.Fatalf("get /stats: %v", err)
	}
	drain(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/stats без ключа: статус %d, ожидался 401", res.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/stats", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err = srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	raw := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/stats с ключом: статус %d, тело %q", res.StatusCode, raw)
	}
	if strings.Contains(raw, testToken) {
		t.Fatalf("токен попал в снимок: %q", raw)
	}
	for _, want := range []string{"opencode/2.2.6", "upstream boom"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("/stats с ключом: нет %q в теле %q", want, raw)
		}
	}
}
