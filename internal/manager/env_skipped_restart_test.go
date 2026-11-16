package manager

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Удаление переменной, без которой набор уже не стартует, не должно его гасить:
// старый процесс продолжает работать со старым окружением, а ответ называет
// причину и набор в skipped.
func TestEnvDeleteSkipsSetThatWouldNotStart(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "db", map[string]string{"TOKEN": "${DB_TOKEN}"})
	id := e.addSet(t, "db", "db")
	if err := e.m.sec.local.set("DB_TOKEN", "s3cret"); err != nil {
		t.Fatalf("env.json: %v", err)
	}
	set := e.m.store.Set(id)
	started, err := e.m.startSet(context.Background(), set)
	if err != nil {
		t.Fatalf("старт: %v", err)
	}
	t.Cleanup(func() { e.m.rt.Stop(set) })

	w := e.do(t, http.MethodDelete, "/api/env/DB_TOKEN", nil)
	status(t, w, http.StatusOK)
	out := decode[struct {
		Restarted []string         `json:"restarted"`
		Skipped   []skippedRestart `json:"skipped"`
	}](t, w)
	if len(out.Restarted) != 0 {
		t.Fatalf("restarted = %v, ждали пусто", out.Restarted)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Set != id || !strings.Contains(out.Skipped[0].Reason, "DB_TOKEN") {
		t.Fatalf("skipped = %+v", out.Skipped)
	}

	// Набор жив и отвечает тем же процессом на своём порту.
	state := decode[struct {
		Sets []struct {
			ID      string `json:"id"`
			Runtime struct {
				Running bool `json:"running"`
			} `json:"runtime"`
		} `json:"sets"`
	}](t, e.do(t, http.MethodGet, "/api/state", nil))
	found := false
	for _, s := range state.Sets {
		if s.ID == id {
			found = true
			if !s.Runtime.Running {
				t.Fatalf("набор остановлен: %s", e.do(t, http.MethodGet, "/api/state", nil).Body.String())
			}
		}
	}
	if !found {
		t.Fatalf("набора %s нет в /api/state", id)
	}
	getApplied(t, started.Port)
}
