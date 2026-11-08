package manager

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestHealthzReflectsDeadAutostart: мёртвый autostart-набор — 503 с его id,
// живой — 200 и счётчик.
func TestHealthzReflectsDeadAutostart(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	w := e.do(t, http.MethodPost, "/api/sets", map[string]any{"name": "dead", "servers": []string{"fake"}, "autostart": true})
	status(t, w, http.StatusCreated)
	id := decode[struct {
		ID string `json:"id"`
	}](t, w).ID

	w = e.do(t, http.MethodGet, "/healthz", nil)
	status(t, w, http.StatusServiceUnavailable)
	if !strings.Contains(w.Body.String(), id) {
		t.Fatalf("в dead нет %s: %s", id, w.Body.String())
	}

	if _, err := e.m.rt.Start(context.Background(), e.m.store.Set(id), e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	defer e.m.rt.Stop(e.m.store.Set(id))

	w = e.do(t, http.MethodGet, "/healthz", nil)
	status(t, w, http.StatusOK)
	if got := decode[map[string]any](t, w); got["status"] != "ok" || got["sets"] != float64(1) {
		t.Fatalf("healthz после старта: %s", w.Body.String())
	}
}
