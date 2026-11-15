package manager

import (
	"net/http"
	"testing"

	"github.com/akrhin/mcprig/internal/stats"
)

// D17: строку статистики `key:<6 hex>` обязана находить панель.
// /api/state отдаёт тот же отпечаток основного ключа (`keyMark`); у открытого
// набора ключа нет — поле пустое.
func TestStateKeyMarkMatchesStatsLabel(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")

	w := e.do(t, http.MethodPost, "/api/sets", map[string]any{"name": "keyed", "servers": []string{"fake"}})
	status(t, w, http.StatusCreated)
	created := decode[struct {
		ID      string `json:"id"`
		Token   string `json:"token"`
		KeyMark string `json:"keyMark"`
	}](t, w)
	want := stats.Fingerprint(created.Token)
	if created.KeyMark != want {
		t.Fatalf("keyMark на создании = %q, ждали %q", created.KeyMark, want)
	}

	// Второй ключ метку не меняет: статистика помечает вызовы отпечатком
	// основного (первого действующего) ключа.
	status(t, e.do(t, http.MethodPost, "/api/sets/"+created.ID+"/keys", nil), http.StatusOK)

	e.addSetBody(t, map[string]any{"name": "open", "servers": []string{"fake"}, "open": true})
	state := decode[struct {
		Sets []struct {
			Name    string `json:"name"`
			KeyMark string `json:"keyMark"`
		} `json:"sets"`
	}](t, e.do(t, http.MethodGet, "/api/state", nil))
	if len(state.Sets) != 2 {
		t.Fatalf("наборов в /api/state: %d, ждали 2", len(state.Sets))
	}
	for _, set := range state.Sets {
		switch set.Name {
		case "keyed":
			if set.KeyMark != want {
				t.Fatalf("keyMark = %q, ждали %q", set.KeyMark, want)
			}
		case "open":
			if set.KeyMark != "" {
				t.Fatalf("у открытого набора keyMark = %q, ждали пусто", set.KeyMark)
			}
		}
	}
}
