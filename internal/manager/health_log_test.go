package manager

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// Журнал получает одну строку на переход в нездоровье, а не на каждый цикл
// опроса health; после выздоровления следующий отказ снова виден.
func TestNoteHealthOnlyOnChange(t *testing.T) {
	var buf bytes.Buffer
	m, err := New(Options{
		DataDir: t.TempDir(),
		Auth:    AuthOptions{Off: true},
		Log:     slog.New(slog.NewTextHandler(&buf, nil)),
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
	m.noteHealth("main", view)
	m.noteHealth("main", view)
	if got := strings.Count(buf.String(), "набор main: сервер qbittorrent unhealthy"); got != 1 {
		t.Fatalf("строк про unhealthy: %d, ожидалась 1; журнал:\n%s", got, buf.String())
	}
	if strings.Contains(buf.String(), "serena unhealthy") {
		t.Fatalf("здоровый сервер попал в журнал: %s", buf.String())
	}

	view.Servers[0].Healthy = &up
	m.noteHealth("main", view)
	if strings.Count(buf.String(), "qbittorrent unhealthy") != 1 {
		t.Fatalf("выздоровление дало строку: %s", buf.String())
	}

	view.Servers[0].Healthy = &down
	m.noteHealth("main", view)
	if got := strings.Count(buf.String(), "qbittorrent unhealthy"); got != 2 {
		t.Fatalf("повторный отказ не записан: %d строк", got)
	}
	if !strings.Contains(buf.String(), "reason=\"нет соединения\"") {
		t.Fatalf("причина не попала в журнал: %s", buf.String())
	}
}
