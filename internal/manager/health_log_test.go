package manager

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// captured — одна запись журнала в том виде, в каком её получил бы journald.
type captured struct {
	msg   string
	attrs map[string]string
}

// captureHandler собирает записи под мьютексом: тест не читает общий
// bytes.Buffer, пока writer пишет в него.
type captureHandler struct {
	mu   sync.Mutex
	recs []captured
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	h.recs = append(h.recs, captured{msg: r.Message, attrs: attrs})
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) find(msg string) (captured, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var found captured
	n := 0
	for _, r := range h.recs {
		if r.msg == msg {
			found = r
			n++
		}
	}
	return found, n
}

// Журнал получает одну строку на переход в нездоровье, а не на каждый цикл
// опроса health; после выздоровления и после сброса кэша следующий отказ снова
// виден.
func TestNoteHealthOnlyOnChange(t *testing.T) {
	h := &captureHandler{}
	m, err := New(Options{
		DataDir: t.TempDir(),
		Auth:    AuthOptions{Off: true},
		Log:     slog.New(h),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)

	down, up := false, true
	view := setHealth{Servers: []serverHealth{
		{Name: "qbittorrent", Healthy: &down, Error: "нет соединения"},
		{Name: "serena", Healthy: &up},
	}}
	const msg = "набор main: сервер qbittorrent unhealthy"

	m.noteHealth("main", view)
	m.noteHealth("main", view)
	rec, n := h.find(msg)
	if n != 1 {
		t.Fatalf("строк про unhealthy: %d, ожидалась 1", n)
	}
	if rec.attrs["reason"] != "нет соединения" {
		t.Fatalf("причина в строке: %+v", rec.attrs)
	}
	if _, n := h.find("набор main: сервер serena unhealthy"); n != 0 {
		t.Fatalf("здоровый сервер попал в журнал: %d строк", n)
	}

	view.Servers[0].Healthy = &up
	m.noteHealth("main", view)
	if _, n := h.find(msg); n != 1 {
		t.Fatalf("выздоровление дало строку: %d", n)
	}

	view.Servers[0].Healthy = &down
	m.noteHealth("main", view)
	if _, n := h.find(msg); n != 2 {
		t.Fatalf("повторный отказ не записан: %d строк", n)
	}

	// Сброс кэша (рестарт набора) забывает состояние: тот же отказ снова виден.
	m.health.clear("main")
	m.noteHealth("main", view)
	if _, n := h.find(msg); n != 3 {
		t.Fatalf("после clear отказ не записан: %d строк", n)
	}
}
