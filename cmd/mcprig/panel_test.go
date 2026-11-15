package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Вход в панель: пароль уходит JSON-ом в /api/login, дальше запросы несут
// cookie, а --secret/--user — заголовки обратного прокси.
func TestPanelLogin(t *testing.T) {
	var mu sync.Mutex
	var loginUser, loginPassword string
	var stateCookie, stateSecret, stateUser string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			User     string `json:"user"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		loginUser, loginPassword = body.User, body.Password
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "oc_session", Value: "ok", Path: "/"})
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if c, err := r.Cookie("oc_session"); err == nil {
			stateCookie = c.Value
		}
		stateSecret = r.Header.Get("x-mcp-proxy-secret")
		stateUser = r.Header.Get("Remote-User")
		mu.Unlock()
		_, _ = w.Write([]byte(`{"sets":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runStatus([]string{
			"--panel", srv.URL,
			"--password", "pw", "--secret", "s3", "--user", "alice",
		})
	})
	if runErr != nil {
		t.Fatalf("status: %v (%q)", runErr, out)
	}

	mu.Lock()
	defer mu.Unlock()
	if loginUser != "alice" || loginPassword != "pw" {
		t.Fatalf("логин: user=%q password=%q", loginUser, loginPassword)
	}
	if stateCookie != "ok" || stateSecret != "s3" || stateUser != "alice" {
		t.Fatalf("запросы с сессией: cookie=%q secret=%q user=%q", stateCookie, stateSecret, stateUser)
	}
}

// Без --secret заголовки обратного прокси не отправляются.
func TestPanelHeadersWithoutSecret(t *testing.T) {
	t.Setenv("MCPRIG_PROXY_SECRET", "")
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]string{
			"secret": r.Header.Get("x-mcp-proxy-secret"),
			"user":   r.Header.Get("Remote-User"),
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	panelHeaders(req, panelCreds{})
	if _, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	}
	if got["secret"] != "" || got["user"] != "" {
		t.Fatalf("лишние заголовки: %v", got)
	}
}
