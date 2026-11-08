package manager

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sintez/mcprig/internal/stats"
)

type callsBody struct {
	Set   string `json:"set"`
	Calls []struct {
		Time   string `json:"time"`
		Set    string `json:"set"`
		Client string `json:"client"`
		Server string `json:"server"`
		Tool   string `json:"tool"`
		MS     int64  `json:"ms"`
		Failed bool   `json:"failed"`
		Error  string `json:"error"`
	} `json:"calls"`
	Counters []struct {
		Client   string `json:"client"`
		Set      string `json:"set"`
		Server   string `json:"server"`
		Tool     string `json:"tool"`
		Calls    int64  `json:"calls"`
		Failures int64  `json:"failures"`
		TotalMs  int64  `json:"totalMs"`
		P50Ms    int64  `json:"p50ms"`
		P95Ms    int64  `json:"p95ms"`
	} `json:"counters"`
	Unreachable []string `json:"unreachable"`
}

// addSetBody заводит набор с произвольным телом (open, port).
func (e *env) addSetBody(t *testing.T, body map[string]any) string {
	t.Helper()
	w := e.do(t, http.MethodPost, "/api/sets", body)
	status(t, w, http.StatusCreated)
	return decode[struct {
		ID string `json:"id"`
	}](t, w).ID
}

// fakeStats отдаёт снимок на /stats и запоминает Authorization последнего
// запроса: так проверяется, что ключ набора уходит, а у открытого — нет.
func fakeStats(t *testing.T, snap stats.Snapshot) (*httptest.Server, *atomic.Value) {
	t.Helper()
	auth := &atomic.Value{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	}))
	t.Cleanup(srv.Close)
	return srv, auth
}

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("разбор адреса фейка: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("порт фейка: %v", err)
	}
	return port
}

// markRunning кладёт в runtime фиктивный живой процесс набора на port.
func markRunning(m *Manager, id string, port int) {
	done := make(chan struct{})
	close(done)
	m.rt.mu.Lock()
	m.rt.procs[id] = &process{port: port, startedAt: time.Now(), done: done}
	m.rt.mu.Unlock()
}

func TestCallsMergeAndLimit(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fs")
	openID := e.addSetBody(t, map[string]any{"name": "open", "servers": []string{"fs"}, "open": true})
	closedID := e.addSetBody(t, map[string]any{"name": "closed", "servers": []string{"fs"}})
	openSet, closedSet := e.m.store.Set(openID), e.m.store.Set(closedID)
	if openSet.Token != "" || closedSet.Token == "" {
		t.Fatalf("ключи наборов: open=%q closed=%q", openSet.Token, closedSet.Token)
	}

	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	openSrv, openAuth := fakeStats(t, stats.Snapshot{
		Set: openID,
		Counters: []stats.Counter{{
			Client: stats.AnonymousLabel, Server: "fix", Tool: "echo",
			Calls: 3, TotalMs: 31, P50Ms: 9, P95Ms: 14,
		}},
		Tail: []stats.Call{
			{Time: base.Add(3 * time.Second), Client: stats.AnonymousLabel, Server: "fix", Tool: "echo", Millis: 12},
			{Time: base.Add(time.Second), Client: stats.AnonymousLabel, Server: "fix", Tool: "echo", Millis: 10},
		},
	})
	closedSrv, closedAuth := fakeStats(t, stats.Snapshot{
		Set: closedID,
		Counters: []stats.Counter{{
			Client: "opencode/2.2.6", Server: "git", Tool: "status",
			Calls: 1, Failures: 1, TotalMs: 40, P50Ms: 40, P95Ms: 40,
		}},
		Tail: []stats.Call{{
			Time: base.Add(2 * time.Second), Client: "opencode/2.2.6", Server: "git", Tool: "status",
			Millis: 40, Failed: true, Message: "boom",
		}},
	})
	markRunning(e.m, openID, serverPort(t, openSrv))
	markRunning(e.m, closedID, serverPort(t, closedSrv))

	w := e.do(t, http.MethodGet, "/api/calls", nil)
	status(t, w, http.StatusOK)
	var got callsBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("разбор /api/calls: %v", err)
	}
	if len(got.Calls) != 3 {
		t.Fatalf("вызовов = %d, ожидалось 3: %s", len(got.Calls), w.Body.String())
	}
	if got.Calls[0].Set != openID || got.Calls[1].Set != closedID || got.Calls[2].Set != openID {
		t.Fatalf("порядок слияния: %+v", got.Calls)
	}
	if !got.Calls[1].Failed || got.Calls[1].Error == "" || got.Calls[0].Failed {
		t.Fatalf("провал должен быть помечен: %+v", got.Calls)
	}
	if got.Calls[1].Error != "boom" || got.Calls[1].MS != 40 {
		t.Fatalf("хвост закрытого набора: %+v", got.Calls[1])
	}
	if len(got.Counters) != 2 {
		t.Fatalf("счётчиков = %d, ожидалось 2: %s", len(got.Counters), w.Body.String())
	}
	var openRow, closedRow bool
	for _, c := range got.Counters {
		switch c.Set {
		case openID:
			openRow = c.Client == stats.AnonymousLabel && c.Calls == 3 && c.TotalMs == 31 && c.P50Ms == 9 && c.P95Ms == 14
		case closedID:
			closedRow = c.Client == "opencode/2.2.6" && c.Failures == 1
		}
	}
	if !openRow || !closedRow {
		t.Fatalf("счётчики после слияния: %+v", got.Counters)
	}
	if openAuth.Load() != "" {
		t.Fatalf("открытый набор получил Authorization %q", openAuth.Load())
	}
	if want := "Bearer " + closedSet.Token; closedAuth.Load() != want {
		t.Fatalf("закрытый набор: Authorization %q, ожидался %q", closedAuth.Load(), want)
	}

	w = e.do(t, http.MethodGet, "/api/calls?set="+closedID, nil)
	status(t, w, http.StatusOK)
	got = decode[callsBody](t, w)
	if got.Set != closedID || len(got.Calls) != 1 || got.Calls[0].Set != "" || len(got.Counters) != 1 {
		t.Fatalf("/api/calls?set=%s: %s", closedID, w.Body.String())
	}

	w = e.do(t, http.MethodGet, "/api/calls?set="+openID+"&limit=1", nil)
	status(t, w, http.StatusOK)
	got = decode[callsBody](t, w)
	if len(got.Calls) != 1 || got.Calls[0].Time != base.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("limit=1: %s", w.Body.String())
	}
}

func TestCallsStoppedAndUnreachable(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fs")
	stoppedID := e.addSetBody(t, map[string]any{"name": "stopped", "servers": []string{"fs"}})
	deadID := e.addSetBody(t, map[string]any{"name": "dead", "servers": []string{"fs"}})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("свободный порт: %v", err)
	}
	freePort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	markRunning(e.m, deadID, freePort)

	w := e.do(t, http.MethodGet, "/api/calls?set="+stoppedID, nil)
	status(t, w, http.StatusOK)
	got := decode[callsBody](t, w)
	if len(got.Calls) != 0 || len(got.Counters) != 0 || len(got.Unreachable) != 0 {
		t.Fatalf("остановленный набор: %s", w.Body.String())
	}

	w = e.do(t, http.MethodGet, "/api/calls?set="+deadID, nil)
	status(t, w, http.StatusOK)
	got = decode[callsBody](t, w)
	if len(got.Calls) != 0 || len(got.Counters) != 0 || len(got.Unreachable) != 1 || got.Unreachable[0] != deadID {
		t.Fatalf("недоступный набор: %s", w.Body.String())
	}

	w = e.do(t, http.MethodGet, "/api/calls", nil)
	status(t, w, http.StatusOK)
	got = decode[callsBody](t, w)
	if len(got.Unreachable) != 1 || got.Unreachable[0] != deadID {
		t.Fatalf("слияние всех наборов: %s", w.Body.String())
	}

	w = e.do(t, http.MethodGet, "/api/calls?set=missing", nil)
	status(t, w, http.StatusNotFound)
}
