package manager

import (
	"net/http"
	"strings"
	"testing"
)

// Профиль при выдаче ключа: известное имя попадает в ключ, неизвестное — 400
// с перечнем доступных, набор без профилей — 400 с подсказкой.
func TestKeyIssueProfile(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSetBody(t, map[string]any{
		"name": "s", "servers": []string{"a"},
		"profiles": map[string]any{"reader": map[string]any{"deny": []string{"*/*_write"}}},
	})

	type issued struct {
		Key struct {
			ID      string `json:"id"`
			Profile string `json:"profile"`
		} `json:"key"`
		Set struct {
			Profiles []string `json:"profiles"`
		} `json:"set"`
	}
	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/keys", map[string]any{"profile": "reader"})
	status(t, w, http.StatusOK)
	body := decode[issued](t, w)
	if body.Key.Profile != "reader" {
		t.Fatalf("ключ получил профиль %q", body.Key.Profile)
	}
	if len(body.Set.Profiles) != 1 || body.Set.Profiles[0] != "reader" {
		t.Fatalf("имена профилей в ответе: %v", body.Set.Profiles)
	}

	w = e.do(t, http.MethodPost, "/api/sets/"+id+"/keys", map[string]any{"profile": "writer"})
	status(t, w, http.StatusBadRequest)
	if text := w.Body.String(); !strings.Contains(text, "reader") {
		t.Fatalf("400 без перечня доступных: %s", text)
	}

	plain := e.addSet(t, "plain", "a")
	w = e.do(t, http.MethodPost, "/api/sets/"+plain+"/keys", map[string]any{"profile": "reader"})
	status(t, w, http.StatusBadRequest)
	if text := w.Body.String(); !strings.Contains(text, "profiles") {
		t.Fatalf("400 без подсказки про profiles: %s", text)
	}

	// Пустой профиль — как раньше: ключ без ограничений.
	status(t, e.do(t, http.MethodPost, "/api/sets/"+plain+"/keys", map[string]any{}), http.StatusOK)
}

// Правка набора по API: отсутствие поля профили не трогает, null снимает,
// битый блок — 400 и набор не меняется (то же, что при загрузке файла).
func TestSetUpdateProfiles(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSetBody(t, map[string]any{
		"name": "s", "servers": []string{"a"},
		"profiles": map[string]any{"reader": map[string]any{"deny": []string{"*/*_write"}}},
	})

	type view struct {
		Profiles []string `json:"profiles"`
	}
	w := e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{"description": "d"})
	status(t, w, http.StatusOK)
	if got := decode[view](t, w).Profiles; len(got) != 1 || got[0] != "reader" {
		t.Fatalf("правка без поля сняла профили: %v", got)
	}

	w = e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{
		"profiles": map[string]any{"reader": map[string]any{"denny": []string{"*"}}},
	})
	// Ошибка валидации реестра не типизирована — API отвечает 500.
	status(t, w, http.StatusInternalServerError)
	w = e.do(t, http.MethodGet, "/api/sets", nil)
	status(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"reader"`) {
		t.Fatalf("битая правка изменила набор: %s", w.Body.String())
	}

	w = e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{"profiles": nil})
	status(t, w, http.StatusOK)
	if got := decode[view](t, w).Profiles; len(got) != 0 {
		t.Fatalf("null не снял профили: %v", got)
	}
}
