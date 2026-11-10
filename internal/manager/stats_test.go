package manager

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sintez/mcprig/internal/stats"
)

// writeHistory кладёт строки в сырой журнал сегодняшних суток набора.
func writeHistory(t *testing.T, dir string, now time.Time, calls ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "raw-"+now.Format("2006-01-02")+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, c := range calls {
		c["time"] = now.Format(time.RFC3339Nano)
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAPIStatsReadsHistory(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fs")
	one := e.addSetBody(t, map[string]any{"name": "one", "servers": []string{"fs"}, "open": true})
	silent := e.addSetBody(t, map[string]any{"name": "silent", "servers": []string{"fs"}, "open": true})

	now := time.Now()
	writeHistory(t, filepath.Join(e.dir, "sets", one, "stats"), now,
		map[string]any{"client": "opencode", "server": "fs", "tool": "echo", "ms": 10, "failed": false},
		map[string]any{"client": "opencode", "server": "fs", "tool": "echo", "ms": 30, "failed": true},
	)

	w := e.do(t, http.MethodGet, "/api/stats?set="+one+"&period=today", nil)
	status(t, w, http.StatusOK)
	var got stats.Result
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("разбор /api/stats: %v", err)
	}
	if got.Totals.Calls != 2 || got.Totals.Failures != 1 || got.Approximate {
		t.Fatalf("today = %+v, approx=%v", got.Totals, got.Approximate)
	}
	if len(got.Unreachable) != 0 {
		t.Fatalf("unreachable = %v", got.Unreachable)
	}
	if len(got.Tools) != 1 || got.Tools[0].Tool != "echo" {
		t.Fatalf("tools = %+v", got.Tools)
	}

	// Набор без каталога истории — пустой ответ с unreachable, не ошибка.
	w = e.do(t, http.MethodGet, "/api/stats?set="+silent+"&period=today", nil)
	status(t, w, http.StatusOK)
	got = stats.Result{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Unreachable) != 1 || got.Unreachable[0] != silent || got.Totals.Calls != 0 {
		t.Fatalf("silent = %+v, unreachable %v", got.Totals, got.Unreachable)
	}

	// set=all складывает наборы.
	w = e.do(t, http.MethodGet, "/api/stats?period=today", nil)
	status(t, w, http.StatusOK)
	got = stats.Result{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Totals.Calls != 2 {
		t.Fatalf("all = %+v", got.Totals)
	}

	status(t, e.do(t, http.MethodGet, "/api/stats?set=nope", nil), http.StatusNotFound)
	status(t, e.do(t, http.MethodGet, "/api/stats?period=year", nil), http.StatusBadRequest)
}
