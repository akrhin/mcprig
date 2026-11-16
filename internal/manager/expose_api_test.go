package manager

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// expose проходит панельный маршрут целиком: POST создаёт, GET отдаёт, PUT
// снимает null, неизвестный вид отбивается 400 с перечнем допустимых.
func TestExposeRoundTripThroughAPI(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})

	w := e.do(t, http.MethodPost, "/api/servers", map[string]any{
		"name":    "alpha",
		"command": []string{"/bin/true"},
		"expose":  []string{"resources"},
	})
	status(t, w, http.StatusCreated)
	if !strings.Contains(w.Body.String(), `"expose":["resources"]`) {
		t.Fatalf("POST без expose в ответе: %s", w.Body.String())
	}

	w = e.do(t, http.MethodGet, "/api/servers", nil)
	status(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"expose":["resources"]`) {
		t.Fatalf("GET не отдаёт expose: %s", w.Body.String())
	}

	w = e.do(t, http.MethodPut, "/api/servers/alpha", map[string]any{"expose": []string{"prompts"}})
	status(t, w, http.StatusOK)
	var srv struct {
		Expose []string `json:"expose"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &srv); err != nil {
		t.Fatalf("PUT: %v (%s)", err, w.Body.String())
	}
	if len(srv.Expose) != 1 || srv.Expose[0] != "prompts" {
		t.Fatalf("expose после PUT: %q", srv.Expose)
	}

	w = e.do(t, http.MethodPut, "/api/servers/alpha", map[string]any{"expose": []string{"ressources"}})
	// Кривой вид поверхности — 400: причина в теле, клиент знает, что править.
	status(t, w, http.StatusBadRequest)

	w = e.do(t, http.MethodPut, "/api/servers/alpha", map[string]any{"expose": nil})
	status(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), `"expose"`) {
		t.Fatalf("null не снял поле: %s", w.Body.String())
	}
}
