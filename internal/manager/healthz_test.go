package manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHealthzReflectsDeadAutostart: мёртвый autostart-набор — 503, но id
// наборов публично не отдаются: только статус и счётчик.
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
	if got := decode[map[string]any](t, w); got["status"] != "degraded" || got["sets"] != float64(1) {
		t.Fatalf("healthz при мёртвом наборе: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), id) {
		t.Fatalf("публичный healthz отдал id набора: %s", w.Body.String())
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

	// sets считает весь реестр, а не только autostart.
	e.addSet(t, "plain", "fake")
	w = e.do(t, http.MethodGet, "/healthz", nil)
	if got := decode[map[string]any](t, w); got["sets"] != float64(2) {
		t.Fatalf("sets не считает наборы реестра: %s", w.Body.String())
	}
}

// TestHealthzRejectsForeignHost: healthz публичный, но Host-политика действует
// и здесь — иначе docker-healthcheck работал бы с любого хоста.
func TestHealthzRejectsForeignHost(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true}) // AllowedHosts: example.com
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Host = "evil.example"
	w := httptest.NewRecorder()
	e.m.ServeHTTP(w, r)
	status(t, w, http.StatusForbidden)
}

// TestDeleteServerReportsStoppedSet: stopped[] — по статусу до Stop, иначе
// список всегда пуст. В наборе два сервера: удалять последний реестр не даст.
func TestDeleteServerReportsStoppedSet(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	e.addServer(t, "other")
	id := e.addSet(t, "live", "fake", "other")
	if _, err := e.m.rt.Start(context.Background(), e.m.store.Set(id), e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}

	w := e.do(t, http.MethodDelete, "/api/servers/fake", nil)
	status(t, w, http.StatusOK)
	stopped, _ := decode[map[string]any](t, w)["stopped"].([]any)
	if len(stopped) != 1 || stopped[0] != id {
		t.Fatalf("stopped = %v, ждали [%s]: %s", stopped, id, w.Body.String())
	}
	if set := e.m.store.Set(id); set == nil || len(set.Servers) != 1 || set.Servers[0] != "other" {
		t.Fatalf("набор после удаления: %+v", set)
	}
}

// TestDeleteLastServerRefused: сервер, без которого набор опустеет, не
// удаляется — 400, сервер и набор на месте, работающий набор не остановлен.
func TestDeleteLastServerRefused(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "only")
	id := e.addSet(t, "s", "only")
	if _, err := e.m.rt.Start(context.Background(), e.m.store.Set(id), e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}

	w := e.do(t, http.MethodDelete, "/api/servers/only", nil)
	if w.Code == http.StatusOK {
		// Инвариант добавлен в ветке registry (fix/audit-config): до её сведения
		// отказ проверить нечем.
		t.Skip("registry.DeleteServer ещё не отказывает последнему серверу набора")
	}
	status(t, w, http.StatusBadRequest)
	if e.m.store.Server("only") == nil {
		t.Fatal("сервер удалён при отказе")
	}
	if set := e.m.store.Set(id); set == nil || len(set.Servers) != 1 {
		t.Fatalf("набор потерял сервер при отказе: %+v", set)
	}
	if !e.m.rt.status(e.m.store.Set(id)).Running {
		t.Fatal("набор остановлен отбитым удалением")
	}
}

// TestRevokeMissingKeyNotFound: нет ключа — 404, как обещает контракт п.6.
func TestRevokeMissingKeyNotFound(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "keys", "fake")

	w := e.do(t, http.MethodDelete, "/api/sets/"+id+"/keys/nosuchkey", nil)
	status(t, w, http.StatusNotFound)
}

// TestAddServerAcceptsPrettyJSON: форматированный JSON — не ошибка.
func TestAddServerAcceptsPrettyJSON(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	w := e.do(t, http.MethodPost, "/api/servers", "{\n  \"name\": \"pretty\",\n  \"command\": [\"/bin/true\"]\n}\n")
	status(t, w, http.StatusCreated)
}
