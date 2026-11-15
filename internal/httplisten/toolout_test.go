package httplisten

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/config"
)

// toolOutName is one dump name: sixteen lowercase hex chars.
const testToolOutName = "0123456789abcdef"

// toolOutSet points the data dir at a temp one, writes one dump and starts a
// set over it. The token and allowAnonymous shape the guard, not the file.
func toolOutSet(t *testing.T, token string, allowAnonymous bool, body string) (*httptest.Server, string) {
	t.Helper()
	t.Setenv("MCPRIG_DATA_DIR", t.TempDir())
	dir := filepath.Join(os.Getenv("MCPRIG_DATA_DIR"), "tool-out")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, testToolOutName+".txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := &config.Config{Transport: &config.HTTPTransport{AuthToken: token, AllowAnonymous: allowAnonymous}}
	h, err := New(cfg, testServer(), slog.New(slog.NewTextHandler(&strings.Builder{}, nil)), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, path
}

// getToolOut asks for one dump; empty token and origin mean "no header".
func getToolOut(t *testing.T, srv *httptest.Server, name, token, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/tool-out/"+name, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

func TestToolOutServesWholeAnswer(t *testing.T) {
	const body = "полный ответ\nвторая строка"
	srv, _ := toolOutSet(t, testToken, false, body)

	res := getToolOut(t, srv, testToolOutName, testToken, "")
	got := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("статус %d, ожидался 200, тело %q", res.StatusCode, got)
	}
	if got != body {
		t.Errorf("тело %q, ожидалось %q", got, body)
	}
	for header, want := range map[string]string{
		"Content-Type":           "text/plain; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
	} {
		if value := res.Header.Get(header); value != want {
			t.Errorf("%s = %q, ожидалось %q", header, value, want)
		}
	}
}

// HEAD says how big a dump is without downloading it: an agent may pull a
// multi-megabyte answer in one slice and wants its length first.
func TestToolOutHead(t *testing.T) {
	const body = "0123456789abcdefghij"
	srv, _ := toolOutSet(t, testToken, false, body)

	req, err := http.NewRequest(http.MethodHead, srv.URL+"/tool-out/"+testToolOutName, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	got := drain(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("HEAD: статус %d, ожидался 200", res.StatusCode)
	}
	if length := res.Header.Get("Content-Length"); length != "20" {
		t.Errorf("Content-Length = %q, ожидалось 20", length)
	}
	if got != "" {
		t.Errorf("HEAD вернул тело %q", got)
	}
}

// A Range request is cut by ServeContent: a client may pull one slice of a
// multi-megabyte dump instead of all of it.
func TestToolOutRange(t *testing.T) {
	srv, _ := toolOutSet(t, testToken, false, "0123456789abcdefghij")

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/tool-out/"+testToolOutName, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Range", "bytes=0-9")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	got := drain(t, res)
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("статус %d, ожидался 206, тело %q", res.StatusCode, got)
	}
	if got != "0123456789" {
		t.Errorf("срез %q, ожидался %q", got, "0123456789")
	}
}

func TestToolOutNeedsKey(t *testing.T) {
	srv, _ := toolOutSet(t, testToken, false, "secret answer")

	res := getToolOut(t, srv, testToolOutName, "", "")
	drain(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("без ключа: статус %d, ожидался 401", res.StatusCode)
	}
}

// An open set lets /mcp through without a key; the dump stays closed: the file
// is not a public endpoint.
func TestToolOutIsClosedEvenInOpenSet(t *testing.T) {
	srv, _ := toolOutSet(t, "", true, "secret answer")

	res := getToolOut(t, srv, testToolOutName, "", "")
	drain(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("открытый набор без ключа: статус %d, ожидался 401", res.StatusCode)
	}
}

func TestToolOutRejectsForeignOrigin(t *testing.T) {
	srv, _ := toolOutSet(t, testToken, false, "answer")

	res := getToolOut(t, srv, testToolOutName, testToken, "http://evil.example")
	drain(t, res)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой Origin: статус %d, ожидался 403", res.StatusCode)
	}
}

func TestToolOutMissingIsNotFound(t *testing.T) {
	srv, _ := toolOutSet(t, testToken, false, "answer")

	for _, name := range []string{"fedcba9876543210", "not-a-name", "../../etc/passwd"} {
		res := getToolOut(t, srv, name, testToken, "")
		drain(t, res)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("%q: статус %d, ожидался 404", name, res.StatusCode)
		}
	}
}

// A dump older than the TTL is gone even before the sweeper removed it: the
// marker's promise has run out, and 410 says exactly that.
func TestToolOutExpiredIsGone(t *testing.T) {
	t.Setenv("MCPRIG_TOOLOUT_TTL", "1h")
	srv, path := toolOutSet(t, testToken, false, "answer")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	res := getToolOut(t, srv, testToolOutName, testToken, "")
	drain(t, res)
	if res.StatusCode != http.StatusGone {
		t.Fatalf("протухший файл: статус %d, ожидался 410", res.StatusCode)
	}
}
