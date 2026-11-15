package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

// expose правится из панели: без этого поля реестр молча выбрасывал его при
// каждой правке сервера, и включить ресурсы/промпты было неоткуда.
func TestSetUpdateExpose(t *testing.T) {
	store := openStore(t, t.TempDir())
	addServer(t, store, `{"name":"alpha","command":["/bin/true"],"expose":["resources"]}`)

	cases := []struct {
		name  string
		patch string
		want  string
		bad   bool
	}{
		{name: "оставить", patch: `{"description":"d"}`, want: "resources"},
		{name: "добавить", patch: `{"expose":["resources","prompts"]}`, want: "resources,prompts"},
		{name: "снять", patch: `{"expose":null}`, want: ""},
		{name: "пусто", patch: `{"expose":[]}`, want: ""},
		{name: "мусор", patch: `{"expose":"resources"}`, want: "", bad: true},
		{name: "опечатка", patch: `{"expose":["ressources"]}`, want: "", bad: true},
	}
	for _, c := range cases {
		srv, err := store.UpdateServer("alpha", json.RawMessage(c.patch))
		if c.bad {
			if err == nil {
				t.Fatalf("%s: ожидалась ошибка, got %q", c.name, srv.Expose)
			}
			if c.name == "опечатка" && !strings.Contains(err.Error(), "ressources") {
				t.Fatalf("%s: ошибка без перечня: %v", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := strings.Join(srv.Expose, ","); got != c.want {
			t.Fatalf("%s: expose=%q, want %q", c.name, got, c.want)
		}
	}
}
