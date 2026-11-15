package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLogsPanel отдаёт первые два запроса хвост, дальше — с новой строкой и
// отменяет follow, чтобы тест вышел сам.
type fakeLogsPanel struct {
	mu      sync.Mutex
	calls   int
	queries []string
	cancel  context.CancelFunc
}

func (f *fakeLogsPanel) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		n := f.calls
		f.queries = append(f.queries, r.URL.Path+"?"+r.URL.RawQuery)
		cancel := f.cancel
		f.mu.Unlock()

		if n >= 3 && cancel != nil {
			cancel()
			return
		}
		lines := []string{"old-1", "old-2"}
		if n >= 2 {
			lines = append(lines, "new-1")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": lines})
	})
}

func TestLogsTail(t *testing.T) {
	f := &fakeLogsPanel{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runLogsContext(context.Background(), &logsConfig{Panel: srv.URL, Set: "main", Lines: 7})
	})
	if runErr != nil {
		t.Fatalf("logs: %v", runErr)
	}
	if out != "old-1\nold-2\n" {
		t.Fatalf("вывод: %q", out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) != 1 || !strings.HasSuffix(f.queries[0], "/api/sets/main/logs?lines=7") {
		t.Fatalf("запросы: %q", f.queries)
	}
}

func TestLogsServerPath(t *testing.T) {
	f := &fakeLogsPanel{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	var runErr error
	_ = captureStdout(t, func() {
		runErr = runLogsContext(context.Background(), &logsConfig{Panel: srv.URL, Set: "main", Server: "alpha", Lines: 200})
	})
	if runErr != nil {
		t.Fatalf("logs: %v", runErr)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) != 1 || !strings.HasSuffix(f.queries[0], "/api/sets/main/servers/alpha/logs?lines=200") {
		t.Fatalf("запросы: %q", f.queries)
	}
}

// -f: первая порция печатается, вторая — только новая строка, выход по
// отмене контекста без ошибки.
func TestLogsFollow(t *testing.T) {
	old := logsPollInterval
	logsPollInterval = 10 * time.Millisecond
	defer func() { logsPollInterval = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeLogsPanel{cancel: cancel}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runLogsContext(ctx, &logsConfig{Panel: srv.URL, Set: "main", Lines: 200, Follow: true})
	})
	if runErr != nil {
		t.Fatalf("follow: %v", runErr)
	}
	if strings.Count(out, "new-1\n") != 1 {
		t.Fatalf("новая строка напечатана не один раз: %q", out)
	}
	if strings.Count(out, "old-1\n") != 1 {
		t.Fatalf("старая строка повторилась: %q", out)
	}
}

func TestLogsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": []any{
			"plain",
			map[string]any{"line": "structured", "ts": "2026-11-15T10:00:00Z"},
		}})
	}))
	defer srv.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runLogsContext(context.Background(), &logsConfig{Panel: srv.URL, Set: "main", Lines: 200, JSON: true})
	})
	if runErr != nil {
		t.Fatalf("logs: %v", runErr)
	}
	rows := strings.Split(strings.TrimSpace(out), "\n")
	if len(rows) != 2 {
		t.Fatalf("строк %d: %q", len(rows), out)
	}
	var plain map[string]any
	if err := json.Unmarshal([]byte(rows[0]), &plain); err != nil {
		t.Fatalf("первая строка не JSON: %v (%q)", err, rows[0])
	}
	if plain["line"] != "plain" || plain["set"] != nil {
		t.Fatalf("простая строка: %v", plain)
	}
	var structured map[string]any
	if err := json.Unmarshal([]byte(rows[1]), &structured); err != nil {
		t.Fatalf("вторая строка не JSON: %v (%q)", err, rows[1])
	}
	if structured["set"] != "main" || structured["line"] != "structured" || structured["ts"] != "2026-11-15T10:00:00Z" {
		t.Fatalf("структура: %v", structured)
	}
}

func TestLogsSetRequired(t *testing.T) {
	if err := runLogsContext(context.Background(), &logsConfig{}); err == nil {
		t.Fatal("без --set должна быть ошибка")
	}
}

// Ctrl-C во время первого запроса — выход без ошибки, а не код 1.
func TestLogsCanceledBeforeFirstFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"logs":["x"]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var runErr error
	out := captureStdout(t, func() {
		runErr = runLogsContext(ctx, &logsConfig{Panel: srv.URL, Set: "main", Lines: 10})
	})
	if runErr != nil {
		t.Fatalf("отмена не должна быть ошибкой: %v", runErr)
	}
	if out != "" {
		t.Fatalf("вывод при отмене: %q", out)
	}
}

func TestNewLogLines(t *testing.T) {
	prev := []logLine{{Line: "a"}, {Line: "b"}}
	if got := newLogLines(prev, []logLine{{Line: "a"}, {Line: "b"}}); len(got) != 0 {
		t.Fatalf("без новых строк: %v", got)
	}
	if got := newLogLines(prev, []logLine{{Line: "b"}, {Line: "c"}}); len(got) != 1 || got[0].Line != "c" {
		t.Fatalf("одна новая: %v", got)
	}
	if got := newLogLines(prev, []logLine{{Line: "x"}, {Line: "y"}}); len(got) != 2 {
		t.Fatalf("хвост перезаписан: %v", got)
	}
}

// Повторяющийся лог: весь ответ — хвост прежнего, но совпадение не значит
// «нового нет» — печатаем ответ целиком, а не тишину.
func TestNewLogLinesRepeatingTail(t *testing.T) {
	a := logLine{Line: "same"}
	b := logLine{Line: "same"}
	if got := newLogLines([]logLine{a, b}, []logLine{a, b}); got != nil {
		t.Fatalf("тот же хвост должен молчать: %v", got)
	}
	got := newLogLines([]logLine{a, b}, []logLine{b})
	if len(got) != 1 || got[0].Line != "same" {
		t.Fatalf("перекрытие съело весь ответ: %v", got)
	}
}
