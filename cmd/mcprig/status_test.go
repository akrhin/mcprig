package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeStatusPanel — минимум панели для status: /healthz, /api/state и
// /api/calls по набору.
func fakeStatusPanel(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sets": []map[string]any{
				{
					"id": "main", "name": "Main", "port": 9101,
					"keys": []map[string]any{{
						"id": "09fadd762726", "createdAt": "2026-11-15T10:00:00Z", "note": "",
					}},
					"keyPrefix": "80e8ffc7",
					"runtime":   map[string]any{"running": true, "port": 9101},
				},
				{
					"id": "lab", "name": "Lab", "port": 9102,
					"keys":    []map[string]any{},
					"runtime": map[string]any{"running": false, "port": 9102},
				},
			},
		})
	})
	mux.HandleFunc("/api/calls", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("set") != "main" {
			t.Errorf("calls без набора: %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"calls":  []map[string]any{{"time": "2026-11-15T10:00:00Z", "server": "alpha", "tool": "run"}},
			"schema": map[string]any{"tools": 12},
		})
	})
	return mux
}

func TestStatusJSONShape(t *testing.T) {
	srv := httptest.NewServer(fakeStatusPanel(t))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runStatus([]string{"--panel", srv.URL, "--json"})
	})
	if runErr != nil {
		t.Fatalf("status: %v (%q)", runErr, out)
	}
	var rep statusReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v (%q)", err, out)
	}
	if rep.Health != "ok" || rep.Panel != srv.URL {
		t.Fatalf("панель/здоровье: %+v", rep)
	}
	if len(rep.Sets) != 2 {
		t.Fatalf("наборов %d, want 2 (%q)", len(rep.Sets), out)
	}
	main := rep.Sets[0]
	if main.ID != "main" || main.State != "running" || main.Port != 9101 || !main.Key {
		t.Fatalf("main: %+v", main)
	}
	if main.Tools != 12 || main.LastCall == "" {
		t.Fatalf("main: инструменты/вызов: %+v", main)
	}
	if lab := rep.Sets[1]; lab.State != "stopped" || lab.Key || lab.Tools != 0 {
		t.Fatalf("lab: %+v", lab)
	}
}

func TestStatusHuman(t *testing.T) {
	srv := httptest.NewServer(fakeStatusPanel(t))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runStatus([]string{"--panel", srv.URL})
	})
	if runErr != nil {
		t.Fatalf("status: %v", runErr)
	}
	for _, want := range []string{"mcprig status", "здоровье: ok", "main", "запущен", "9101", "12", "есть", "—", "итог: 2 набора"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
}

func TestStatusSetFilter(t *testing.T) {
	srv := httptest.NewServer(fakeStatusPanel(t))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runStatus([]string{"--panel", srv.URL, "--set", "lab"})
	})
	if runErr != nil {
		t.Fatalf("status: %v", runErr)
	}
	if strings.Contains(out, "main") || !strings.Contains(out, "lab") {
		t.Fatalf("фильтр не сработал:\n%s", out)
	}
	if !strings.Contains(out, "итог: 1 набор") {
		t.Fatalf("итог: %q", out)
	}
}

// Отозванный ключ остаётся в /api/state, но выданным не считается.
func TestStatusRevokedKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sets": []map[string]any{{
			"id": "old", "name": "Old", "port": 9103,
			"keys": []map[string]any{
				{"id": "aaaaaaaaaaaa", "createdAt": "2026-11-01T10:00:00Z", "revokedAt": "2026-11-10T10:00:00Z"},
				{"id": "bbbbbbbbbbbb", "createdAt": "2026-11-10T10:00:00Z", "revokedAt": "2026-11-12T10:00:00Z"},
			},
			"runtime": map[string]any{"running": false, "port": 9103},
		}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runStatus([]string{"--panel", srv.URL, "--json"})
	})
	if runErr != nil {
		t.Fatalf("status: %v", runErr)
	}
	var rep statusReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v (%q)", err, out)
	}
	if len(rep.Sets) != 1 || rep.Sets[0].Key {
		t.Fatalf("у набора только отозванные ключи: %+v", rep.Sets)
	}
}

// Пустая панель: в -json "sets": [], а не null.
func TestStatusEmptySetsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		_, _ = w.Write([]byte(`{"sets":[]}`))
	}))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runStatus([]string{"--panel", srv.URL, "--json"})
	})
	if runErr != nil {
		t.Fatalf("status: %v", runErr)
	}
	if !strings.Contains(out, `"sets": []`) {
		t.Fatalf("ожидался пустой массив, а не null:\n%s", out)
	}
}

func TestStatusUnknownSet(t *testing.T) {
	srv := httptest.NewServer(fakeStatusPanel(t))
	defer srv.Close()

	if err := runStatus([]string{"--panel", srv.URL, "--set", "nosuch"}); err == nil {
		t.Fatal("неизвестный набор должен быть ошибкой")
	}
}

// 401 на /api/state — ошибка с адресом панели, а не паника.
func TestStatusUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"нужна авторизация","login":true}`))
	}))
	defer srv.Close()

	err := runStatus([]string{"--panel", srv.URL})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("err = %v, want 401 и адрес панели", err)
	}
}

// Мёртвая панель — ошибка с адресом.
func TestStatusPanelDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	err := runStatus([]string{"--panel", url})
	if err == nil || !strings.Contains(err.Error(), url) {
		t.Fatalf("err = %v, want адрес панели", err)
	}
}
