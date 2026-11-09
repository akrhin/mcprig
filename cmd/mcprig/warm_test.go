package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseArgsWarm(t *testing.T) {
	mode, rest, err := parseArgs([]string{"warm", "-json", "--set", "main"})
	if err != nil || mode != "warm" || len(rest) != 3 {
		t.Fatalf("warm: mode=%q rest=%q err=%v", mode, rest, err)
	}
	if _, _, err := parseArgs([]string{"warm", "лишнее"}); err == nil {
		t.Fatal("warm: лишний позиционный аргумент должен быть ошибкой")
	}
}

// captureStdout уже есть в init_test.go: подкоманды печатают отчёт сами,
// поэтому проверять надо строку вывода, а ошибку — отдельной переменной.

// Без токена прогрев не начинается: ошибка говорит, где взять ключ.
func TestWarmNoToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MCPRIG_TOKEN", "")
	var runErr error
	out := captureStdout(t, func() {
		runErr = runWarm([]string{"--url", "http://127.0.0.1:1/mcp", "--set", "nosuch"})
	})
	if !errors.Is(runErr, errWarmProblems) {
		t.Fatalf("err = %v, want errWarmProblems", runErr)
	}
	if !strings.Contains(out, "нет токена набора") || !strings.Contains(out, "mcprig init") {
		t.Fatalf("вывод не подсказывает mcprig init: %q", out)
	}
}

// fakeWarmServer отвечает на минимум streamable HTTP: server/discover
// отклоняется (клиент уходит на legacy initialize), list_servers и один
// list_commands. Вызовы и их аргументы пишутся в calls.
func fakeWarmServer(calls *[]string) http.Handler {
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &msg)
		w.Header().Set("Content-Type", "application/json")
		if len(msg.ID) == 0 { // notification
			w.WriteHeader(http.StatusAccepted)
			return
		}
		reply := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
		}
		switch msg.Method {
		case "server/discover":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"}})
		case "initialize":
			reply(map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake", "version": "1"},
			})
		case "tools/call":
			mu.Lock()
			*calls = append(*calls, msg.Params.Name+" "+string(msg.Params.Arguments))
			mu.Unlock()
			switch msg.Params.Name {
			case "list_servers":
				reply(map[string]any{"content": []map[string]any{{"type": "text",
					"text": `{"servers":[{"name":"alpha"},{"name":"beta"}]}`}}})
			case "list_commands":
				reply(map[string]any{"content": []map[string]any{{"type": "text",
					"text": `{"alpha":[{"server":"alpha","name":"a1"}],"beta":[{"server":"beta","name":"b1"},{"server":"beta","name":"b2"}]}`}}})
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	})
}

// Успешный прогрев: ровно один list_commands, без refresh, и три инструмента
// в строке отчёта.
func TestWarmFakeHTTP(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(fakeWarmServer(&calls))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runWarm([]string{"--url", srv.URL, "--set", "main", "--token", "ghost"})
	})
	if runErr != nil {
		t.Fatalf("warm: %v (%q)", runErr, out)
	}
	if !strings.Contains(out, "main: 3 инструмента") {
		t.Fatalf("строка отчёта: %q", out)
	}
	if !strings.Contains(out, "итог: 1 набор") {
		t.Fatalf("нет итога: %q", out)
	}
	lists := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "list_commands ") {
			lists++
			if strings.Contains(call, "refresh") {
				t.Fatalf("list_commands передал refresh: %q", call)
			}
			if !strings.Contains(call, "alpha") || !strings.Contains(call, "beta") {
				t.Fatalf("list_commands без имён серверов: %q", call)
			}
		}
	}
	if lists != 1 {
		t.Fatalf("list_commands вызван %d раз, want 1 (%q)", lists, calls)
	}
}

// Панель недоступна (500) — отчёт с ошибкой, код 1, без паники.
func TestWarmAllPanelDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runWarm([]string{"--all", "--panel", srv.URL})
	})
	if !errors.Is(runErr, errWarmProblems) {
		t.Fatalf("err = %v, want errWarmProblems", runErr)
	}
	if !strings.Contains(out, "500") {
		t.Fatalf("в выводе нет кода панели: %q", out)
	}
}

// Токен по умолчанию берётся из ~/.config/mcprig/<set>.token.
func TestWarmTokenFileDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("MCPRIG_TOKEN", "")
	path := filepath.Join(dir, "mcprig", "main.token")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("  from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := warmToken(&warmConfig{}, "main")
	if err != nil || tok != "from-file" {
		t.Fatalf("token = %q, err = %v", tok, err)
	}
}

// Открытый набор из панели помечается, поэтому прогревается без ключа.
func TestEndpointForMarksOpenSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "main", "port": 9101, "autostart": true, "open": true}})
	}))
	defer srv.Close()

	o := &warmConfig{Panel: srv.URL}
	endpoint, err := endpointFor(context.Background(), o, "main")
	if err != nil || endpoint != "http://127.0.0.1:9101/mcp" || !o.setOpen {
		t.Fatalf("endpoint=%q open=%v err=%v", endpoint, o.setOpen, err)
	}
}
