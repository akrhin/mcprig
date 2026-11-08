package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Без OpenBao-записи пустой store удаляет из env.json и говорит об этом.
func TestDeleteWithoutStoreFallsBackToFile(t *testing.T) {
	fake := &fakeBao{values: map[string]string{}}
	sec := fakeSecrets(t, fake, map[string]string{"BAR": "file"})

	removed, source, err := sec.DeleteFrom("BAR", "")
	if err != nil || !removed || source != sourceFile {
		t.Fatalf("DeleteFrom = %v, %q, %v", removed, source, err)
	}
	if _, source := sec.Get("BAR"); source != "" {
		t.Fatalf("переменная всё ещё видна из %q", source)
	}
}

// DELETE без store убирает обе копии: GET /api/env больше не показывает
// переменную, и оставшийся файл её не воскрешает.
func TestEnvDeleteWithoutStoreHidesVariable(t *testing.T) {
	fake := &fakeBao{values: map[string]string{"FOO": "remote"}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	t.Setenv("BAO_ADDR", srv.URL)
	t.Setenv("BAO_TOKEN", "t")
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "db", map[string]string{"OWN": "${FOO}"})
	if err := e.m.sec.local.set("FOO", "file"); err != nil {
		t.Fatalf("env.json: %v", err)
	}

	status(t, e.do(t, http.MethodDelete, "/api/env/FOO", nil), http.StatusOK)

	var out struct {
		Vars []struct {
			Name string `json:"name"`
			Set  bool   `json:"set"`
		} `json:"vars"`
	}
	w := e.do(t, http.MethodGet, "/api/env", nil)
	status(t, w, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET /api/env: %v", err)
	}
	for _, v := range out.Vars {
		if v.Name == "FOO" && v.Set {
			t.Fatalf("после DELETE переменная всё ещё видна: %s", w.Body.String())
		}
	}
}

// Контракт TS-эталона зовёт файловый источник `local`; принимаем оба имени.
func TestStoreLocalAlias(t *testing.T) {
	fake := &fakeBao{values: map[string]string{}}
	sec := fakeSecrets(t, fake, nil)

	if source, err := sec.Set("NEW_VAR", "v", "local"); err != nil || source != sourceFile {
		t.Fatalf("Set store=local: источник %q, ошибка %v", source, err)
	}
	if v, _ := sec.Get("NEW_VAR"); v != "v" {
		t.Fatalf("Get = %q, ожидалось v", v)
	}
	removed, source, err := sec.DeleteFrom("NEW_VAR", "local")
	if err != nil || !removed || source != sourceFile {
		t.Fatalf("DeleteFrom store=local: removed=%v источник %q ошибка %v", removed, source, err)
	}
}
