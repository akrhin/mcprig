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
	"github.com/akrhin/mcprig/internal/stats"
	"github.com/akrhin/mcprig/internal/toolout"
)

// toolOutName is one dump name: sixteen lowercase hex chars.
const testToolOutName = "0123456789abcdef"

// otherToken — второй ключ того же набора: им читают чужую по владельцу выгрузку.
const otherToken = "second-key"

// toolOutSet points the data dir at a temp one, writes one dump and starts a
// set over it. The token and allowAnonymous shape the guard, not the file; the
// owner sidecar belongs to the key that would have asked for the dump.
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
	writeOwner(t, path, token)
	cfg := &config.Config{Transport: &config.HTTPTransport{AuthToken: token, AllowAnonymous: allowAnonymous}}
	h, err := New(cfg, testServer(), slog.New(slog.NewTextHandler(&strings.Builder{}, nil)), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, path
}

// writeOwner puts the sidecar beside a dump: the key fingerprint, or the
// anonymous mark a caller without a key leaves.
func writeOwner(t *testing.T, path, token string) {
	t.Helper()
	owner := toolout.AnonymousOwner
	if token != "" {
		owner = stats.Fingerprint(token)
	}
	if err := os.WriteFile(toolout.OwnerPath(path), []byte(owner+"\n"), 0o600); err != nil {
		t.Fatalf("owner: %v", err)
	}
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

// twoKeySet starts a set that accepts both keys and writes one dump owned by
// the first: the second key is a legitimate caller of the same set.
func twoKeySet(t *testing.T, body string) (*httptest.Server, string) {
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
	writeOwner(t, path, testToken)
	cfg := &config.Config{Transport: &config.HTTPTransport{AuthTokens: []string{testToken, otherToken}}}
	h, err := New(cfg, testServer(), slog.New(slog.NewTextHandler(&strings.Builder{}, nil)), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, path
}

// The dump belongs to the key whose call produced it: another key of the same
// set gets 404 on a known name, and the file stays for its owner.
func TestToolOutForeignKeyIsNotFound(t *testing.T) {
	srv, path := twoKeySet(t, "чужой ответ")

	res := getToolOut(t, srv, testToolOutName, otherToken, "")
	got := drain(t, res)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("чужой ключ: статус %d, ожидался 404, тело %q", res.StatusCode, got)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "чужой ответ" {
		t.Fatalf("файл владельца пострадал: %q, %v", data, err)
	}

	res = getToolOut(t, srv, testToolOutName, testToken, "")
	got = drain(t, res)
	if res.StatusCode != http.StatusOK || got != "чужой ответ" {
		t.Fatalf("свой ключ: статус %d, тело %q", res.StatusCode, got)
	}
}

// A dump without its owner sidecar is nobody's: 404, not a fallback to "any key
// reads it".
func TestToolOutMissingOwnerIsNotFound(t *testing.T) {
	srv, path := toolOutSet(t, testToken, false, "answer")
	if err := os.Remove(toolout.OwnerPath(path)); err != nil {
		t.Fatalf("remove owner: %v", err)
	}

	res := getToolOut(t, srv, testToolOutName, testToken, "")
	drain(t, res)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("без .owner: статус %d, ожидался 404", res.StatusCode)
	}
}

// An open set has no keys, so its dumps are anonymous on both sides: the owner
// gate lets a caller without a key read what a caller without a key wrote. The
// route itself stays behind the guard (TestToolOutIsClosedEvenInOpenSet).
func TestToolOutHandlerServesAnonymousDump(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testToolOutName+".txt")
	if err := os.WriteFile(path, []byte("ответ открытого набора"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeOwner(t, path, "")

	req := httptest.NewRequest(http.MethodGet, "/tool-out/"+testToolOutName, nil)
	req.SetPathValue("name", testToolOutName)
	w := httptest.NewRecorder()
	toolOutHandler(dir, time.Hour)(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "ответ открытого набора" {
		t.Fatalf("статус %d, тело %q", w.Code, w.Body.String())
	}
}

// The set takes its retention from the registry, not from the environment: the
// marker's promise and the sweep must agree, and a set under the panel gets no
// MCPRIG_TOOLOUT_TTL at all.
func TestToolOutTTLFromSetConfig(t *testing.T) {
	t.Setenv("MCPRIG_DATA_DIR", t.TempDir())
	dir := filepath.Join(os.Getenv("MCPRIG_DATA_DIR"), "tool-out")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, testToolOutName+".txt")
	if err := os.WriteFile(path, []byte("answer"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeOwner(t, path, testToken)
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	cfg := &config.Config{Transport: &config.HTTPTransport{AuthToken: testToken, ToolOutTTL: "1h"}}
	h, err := New(cfg, testServer(), slog.New(slog.NewTextHandler(&strings.Builder{}, nil)), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Окружение молчит: срок 24 ч дал бы 200, а конфиг набора — 1 ч, значит 410.
	res := getToolOut(t, srv, testToolOutName, testToken, "")
	drain(t, res)
	if res.StatusCode != http.StatusGone {
		t.Fatalf("срок из набора: статус %d, ожидался 410", res.StatusCode)
	}
}
