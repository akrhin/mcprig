package manager

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
)

// Journald не должен зависеть от открытой панели: фоновый опрос сам находит
// упавший сервер, пишет строку на переходе и молчит, пока состояние прежнее.
func TestHealthLoopLogsWithoutAPI(t *testing.T) {
	h := &captureHandler{}
	var bad atomic.Bool
	bad.Store(true)
	dir := t.TempDir()
	m, err := New(Options{
		DataDir:     dir,
		Version:     "test",
		CLIPath:     fakeBin,
		Auth:        AuthOptions{Off: true, AllowedHosts: []string{"example.com"}},
		Log:         slog.New(h),
		healthEvery: 10 * time.Millisecond,
		probe: func(_ context.Context, set *registry.Set) setHealth {
			out := setHealth{Servers: make([]serverHealth, len(set.Servers))}
			for i, name := range set.Servers {
				healthy := !bad.Load()
				out.Servers[i] = serverHealth{Name: name, Healthy: &healthy}
				if !healthy {
					out.Servers[i].Error = "нет соединения"
				}
			}
			return out
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)

	e := &env{m: m, dir: dir}
	e.addServer(t, "fake")
	id := e.addSet(t, "loop", "fake")
	set := m.store.Set(id)
	if _, err := m.startSet(context.Background(), set); err != nil {
		t.Fatalf("старт набора: %v", err)
	}
	t.Cleanup(func() { m.rt.Stop(set) })

	const msg = "набор loop: сервер fake unhealthy"
	waitLines := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, n := h.find(msg); n == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		_, n := h.find(msg)
		t.Fatalf("строк про unhealthy: %d, ожидалось %d", n, want)
	}

	// startSet чистит кэш здоровья: строки до старта не считаем, ждём первую
	// после него — ни одного запроса к API за здоровьем при этом не было.
	_, base := h.find(msg)
	waitLines(base + 1)
	time.Sleep(50 * time.Millisecond)
	if _, n := h.find(msg); n != base+1 {
		t.Fatalf("строка задублировалась на тиках: %d", n)
	}

	bad.Store(false)
	time.Sleep(50 * time.Millisecond)
	bad.Store(true)
	waitLines(base + 2)
}
