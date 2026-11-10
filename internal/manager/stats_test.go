package manager

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/stats"
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

// Фильтры из строки запроса доходят до QueryHistory, а set=all называет
// набор без каталога истории, как обещает док.
func TestAPIStatsFiltersAndAll(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fs")
	one := e.addSetBody(t, map[string]any{"name": "one", "servers": []string{"fs"}, "open": true})
	silent := e.addSetBody(t, map[string]any{"name": "silent", "servers": []string{"fs"}, "open": true})
	now := time.Now()
	writeHistory(t, filepath.Join(e.dir, "sets", one, "stats"), now,
		map[string]any{"client": "opencode", "server": "fs", "tool": "echo", "ms": 10, "failed": false},
		map[string]any{"client": "claude", "server": "other", "tool": "fetch", "ms": 50, "failed": true},
	)

	w := e.do(t, http.MethodGet, "/api/stats?set="+one+"&period=today&server=fs&tool=echo", nil)
	status(t, w, http.StatusOK)
	var got stats.Result
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("разбор /api/stats: %v", err)
	}
	if got.Totals.Calls != 1 || got.Totals.Failures != 0 {
		t.Fatalf("фильтр не применён: %+v", got.Totals)
	}
	if len(got.Tools) != 1 || got.Tools[0].Tool != "echo" {
		t.Fatalf("tools = %+v", got.Tools)
	}

	w = e.do(t, http.MethodGet, "/api/stats?set=all&period=today&server=fs&tool=echo", nil)
	status(t, w, http.StatusOK)
	got = stats.Result{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Totals.Calls != 1 {
		t.Fatalf("set=all с фильтром: %+v", got.Totals)
	}
	if len(got.Unreachable) != 1 || got.Unreachable[0] != silent {
		t.Fatalf("unreachable при set=all = %v, ожидался %q", got.Unreachable, silent)
	}
}

// Шаг ряда ограничен hour|day|month: опечатка не должна молча давать дефолт.
func TestAPIStatsBucketValidation(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	status(t, e.do(t, http.MethodGet, "/api/stats?period=today&bucket=year", nil), http.StatusBadRequest)
}

// Таблица наборов: set=all отдаёт счётчики по каждому набору тем же проходом;
// набор без истории не получает нулевую строку, а один набор разреза не несёт.
func TestAPIStatsBySet(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fs")
	one := e.addSetBody(t, map[string]any{"name": "one", "servers": []string{"fs"}, "open": true})
	two := e.addSetBody(t, map[string]any{"name": "two", "servers": []string{"fs"}, "open": true})
	silent := e.addSetBody(t, map[string]any{"name": "silent", "servers": []string{"fs"}, "open": true})

	now := time.Now()
	writeHistory(t, filepath.Join(e.dir, "sets", one, "stats"), now,
		map[string]any{"client": "opencode", "server": "fs", "tool": "echo", "ms": 10, "failed": false},
		map[string]any{"client": "opencode", "server": "fs", "tool": "echo", "ms": 30, "failed": true},
	)
	writeHistory(t, filepath.Join(e.dir, "sets", two, "stats"), now,
		map[string]any{"client": "claude", "server": "fs", "tool": "fetch", "ms": 200, "failed": false},
	)

	w := e.do(t, http.MethodGet, "/api/stats?set=all&period=today", nil)
	status(t, w, http.StatusOK)
	var got stats.Result
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("разбор /api/stats: %v", err)
	}
	want := []stats.SetRow{
		{Set: one, Calls: 2, Failures: 1, TotalMs: 40},
		{Set: two, Calls: 1, Failures: 0, TotalMs: 200},
	}
	if len(got.BySet) != len(want) {
		t.Fatalf("bySet = %+v, ожидалось %+v", got.BySet, want)
	}
	for i := range want {
		if got.BySet[i] != want[i] {
			t.Fatalf("bySet[%d] = %+v, ожидалось %+v", i, got.BySet[i], want[i])
		}
	}
	if len(got.Unreachable) != 1 || got.Unreachable[0] != silent {
		t.Fatalf("unreachable = %v, ожидался %q", got.Unreachable, silent)
	}
	var sum int64
	for _, r := range got.BySet {
		sum += r.Calls
	}
	if sum != got.Totals.Calls {
		t.Fatalf("bySet складывается в %d, totals = %d", sum, got.Totals.Calls)
	}

	// Один набор: разрез не нужен, поле пустое.
	w = e.do(t, http.MethodGet, "/api/stats?set="+one+"&period=today", nil)
	status(t, w, http.StatusOK)
	got = stats.Result{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.BySet) != 0 {
		t.Fatalf("bySet у одного набора = %+v", got.BySet)
	}
}
