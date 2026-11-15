package manager

import (
	"context"
	"net/http"
	"testing"

	"github.com/akrhin/mcprig/internal/registry"
)

// Проба не дала ответа — серверы всё равно в ответе, с ошибкой: 200 с
// servers: null панель читает как «серверов нет» (§C11).
func TestSetServersKeepsServersWhenProbeEmpty(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "probe", "fake")
	if _, err := e.m.rt.Start(context.Background(), e.m.store.Set(id), e.m.store.Servers()); err != nil {
		t.Fatalf("старт набора: %v", err)
	}
	e.m.probe = func(context.Context, *registry.Set) setHealth { return setHealth{} }

	w := e.do(t, http.MethodGet, "/api/sets/"+id+"/servers", nil)
	status(t, w, http.StatusOK)
	view := decode[setHealth](t, w)
	if len(view.Servers) != 1 || view.Servers[0].Name != "fake" {
		t.Fatalf("сервер исчез из ответа: %s", w.Body.String())
	}
	if view.Servers[0].Error == "" {
		t.Fatalf("у сервера нет ошибки пробы: %s", w.Body.String())
	}
}
