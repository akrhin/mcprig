package main

// mcprig approve: разбор аргументов (позиционные server/tool, --all) и вход в
// панель. Панель с аутентификацией проверяется настоящая: CSRF требует Origin,
// и CLI обязан слать его как браузер.

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/akrhin/mcprig/internal/manager"
	"github.com/akrhin/mcprig/internal/toollock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// panelRecorder — панель-двойник: записывает запросы одобрения и отвечает так,
// как велел тест. POST без Origin отбивает, как checkCSRF настоящей панели.
type panelRecorder struct {
	mu     sync.Mutex
	bodies []string
	reply  func() (int, string)
}

func (p *panelRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "oc_ui_session_9091", Value: "s", Path: "/"})
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Origin") == "" {
			http.Error(w, `{"error":"не хватает заголовка Origin"}`, http.StatusForbidden)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.bodies = append(p.bodies, string(raw))
		p.mu.Unlock()
		code, body := http.StatusOK, `{"approved":[]}`
		if p.reply != nil {
			code, body = p.reply()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

func newFakePanel(t *testing.T, reply func() (int, string)) (*httptest.Server, *panelRecorder) {
	t.Helper()
	p := &panelRecorder{reply: reply}
	srv := httptest.NewServer(p.handler())
	t.Cleanup(srv.Close)
	return srv, p
}

func TestApprovePositionalTargets(t *testing.T) {
	srv, panel := newFakePanel(t, func() (int, string) { return http.StatusOK, `{"approved":["x"]}` })

	err := runApprove([]string{"-panel", srv.URL, "-password", "pw", "-set", "main", "alpha/echo", "beta/x"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(panel.bodies) != 2 {
		t.Fatalf("запросов %d, ожидалось 2: %+v", len(panel.bodies), panel.bodies)
	}
	for i, want := range []string{`"server":"alpha"`, `"server":"beta"`} {
		if !strings.Contains(panel.bodies[i], want) {
			t.Fatalf("тело %d: %s", i, panel.bodies[i])
		}
	}
}

func TestApproveAllSendsOneRequest(t *testing.T) {
	srv, panel := newFakePanel(t, func() (int, string) { return http.StatusOK, `{"approved":["a/x","b/y"]}` })

	if err := runApprove([]string{"-panel", srv.URL, "-password", "pw", "-all"}); err != nil {
		t.Fatalf("approve --all: %v", err)
	}
	if len(panel.bodies) != 1 || !strings.Contains(panel.bodies[0], `"all":true`) {
		t.Fatalf("--all ушёл не одним запросом: %+v", panel.bodies)
	}
}

// Ничего не менялось — не ошибка: одобрять нечего.
func TestApproveAllWithNothingToDo(t *testing.T) {
	srv, _ := newFakePanel(t, func() (int, string) { return http.StatusOK, `{"approved":[]}` })
	if err := runApprove([]string{"-panel", srv.URL, "-password", "pw", "-all"}); err != nil {
		t.Fatalf("пустой --all: %v", err)
	}
}

// Неизвестный или неизменившийся инструмент — отказ: «одобрено» без записи
// обмануло бы человека.
func TestApproveUnknownToolFails(t *testing.T) {
	srv, _ := newFakePanel(t, func() (int, string) {
		return http.StatusConflict, `{"error":"нечего одобрять: alpha/nope не менялся или неизвестен"}`
	})
	err := runApprove([]string{"-panel", srv.URL, "-password", "pw", "alpha/nope"})
	if err == nil {
		t.Fatal("отказ панели проглочен")
	}
}

func TestApproveUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--all", "alpha/echo"},
		{},
		{"alpha"},
	} {
		var usage *usageError
		if err := runApprove(args); !errors.As(err, &usage) {
			t.Fatalf("%v: ожидалась ошибка использования, получено %v", args, err)
		}
	}
}

// Целиком через настоящую панель с паролем: вход, Origin против CSRF, запись
// одобрения в файл набора.
func TestApproveThroughPanelWithAuth(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := manager.New(manager.Options{
		DataDir: dir,
		Version: "test",
		CLIPath: "/bin/true",
		Auth:    manager.AuthOptions{Password: "pw"},
		Log:     log,
	})
	if err != nil {
		t.Fatalf("менеджер: %v", err)
	}
	t.Cleanup(mgr.Close)
	srv := httptest.NewServer(mgr)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	post := func(path string, body any, origin bool) *http.Response {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if origin {
			req.Header.Set("Origin", srv.URL)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	if res := post("/api/login", map[string]string{"user": "admin", "password": "pw"}, false); res.StatusCode != http.StatusOK {
		t.Fatalf("вход: %d", res.StatusCode)
	}
	if res := post("/api/servers", map[string]any{"name": "alpha", "command": []string{"downstream"}}, true); res.StatusCode != http.StatusCreated {
		t.Fatalf("сервер: %d", res.StatusCode)
	}
	res := post("/api/sets", map[string]any{"name": "quar", "servers": []string{"alpha"}}, true)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("набор: %d", res.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("набор: %v", err)
	}

	path := toollock.Path(dir, created.ID)
	v1 := toollock.Hash(&mcp.Tool{Name: "echo", Description: "v1"})
	v2 := toollock.Hash(&mcp.Tool{Name: "echo", Description: "v2"})
	if err := toollock.Write(path, toollock.File{Tools: map[string]toollock.Entry{
		"alpha/echo": {Hash: v1, Seen: v2, ApprovedBy: "auto"},
	}}); err != nil {
		t.Fatalf("запись карантина: %v", err)
	}

	if err := runApprove([]string{"-panel", srv.URL, "-password", "pw", "-set", created.ID, "alpha/echo"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := toollock.Read(path).Tools["alpha/echo"]; got.Hash != v2 || got.Seen != "" || got.ApprovedBy != "panel" {
		t.Fatalf("одобрение не записано: %+v", got)
	}
}
