package manager

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/stats"
)

// D17: строку статистики `key:<6 hex>` обязана находить панель.
// /api/state отдаёт ту же метку основного ключа (`keyMark`) — отпечаток, а у
// подписанного ключа подпись; у открытого набора ключа нет — поле пустое.
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
	want := "key:" + stats.Fingerprint(created.Token)
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

// Подписанный ключ виден в статистике подписью (её подставляет сам набор), и
// keyMark обязан быть той же строкой, а не отпечатком.
func TestStateKeyMarkUsesKeyLabel(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "signed", "fake")

	w := e.do(t, http.MethodGet, "/api/sets/"+id+"/keys", nil)
	status(t, w, http.StatusOK)
	keys := decode[struct {
		Keys []struct {
			ID string `json:"id"`
		} `json:"keys"`
	}](t, w)
	if len(keys.Keys) == 0 {
		t.Fatal("у набора нет ключа")
	}
	status(t, e.do(t, http.MethodPatch, "/api/sets/"+id+"/keys/"+keys.Keys[0].ID, map[string]any{"note": "ci"}), http.StatusOK)

	writeHistory(t, filepath.Join(e.dir, "sets", id, "stats"), time.Now(),
		map[string]any{"client": "ci", "server": "fake", "tool": "echo", "ms": 5, "failed": false},
	)

	state := decode[struct {
		Sets []struct {
			ID      string `json:"id"`
			KeyMark string `json:"keyMark"`
		} `json:"sets"`
	}](t, e.do(t, http.MethodGet, "/api/state", nil))
	mark := ""
	for _, set := range state.Sets {
		if set.ID == id {
			mark = set.KeyMark
		}
	}
	if mark != "ci" {
		t.Fatalf("keyMark = %q, ждали подпись ci", mark)
	}

	w = e.do(t, http.MethodGet, "/api/stats?set="+id+"&period=today", nil)
	status(t, w, http.StatusOK)
	got := decode[struct {
		Totals struct {
			Calls int64 `json:"calls"`
		} `json:"totals"`
		Clients []struct {
			Client string `json:"client"`
		} `json:"clients"`
	}](t, w)
	if got.Totals.Calls != 1 || len(got.Clients) != 1 || got.Clients[0].Client != mark {
		t.Fatalf("статистика %+v не совпала с keyMark %q", got, mark)
	}
}
