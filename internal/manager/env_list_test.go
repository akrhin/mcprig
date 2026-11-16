package manager

import (
	"encoding/json"
	"net/http"
	"testing"
)

type envListRow struct {
	Name   string `json:"name"`
	Set    bool   `json:"set"`
	Used   bool   `json:"used"`
	Source string `json:"source"`
}

func envList(t *testing.T, e *env) []envListRow {
	t.Helper()
	w := e.do(t, http.MethodGet, "/api/env", nil)
	status(t, w, http.StatusOK)
	var out struct {
		Vars []envListRow `json:"vars"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET /api/env: %v", err)
	}
	return out.Vars
}

// Сохранённое значение без серверных ссылок видно в панели с пометкой used:false
// и стоит после используемых: иначе забыть его можно только зная имя наизусть.
func TestEnvListShowsStoredButUnused(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "db", map[string]string{"OWN": "${A_USED}"})
	if err := e.m.sec.local.set("LEFTOVER", "v"); err != nil {
		t.Fatalf("env.json: %v", err)
	}

	rows := envList(t, e)
	if len(rows) != 2 {
		t.Fatalf("строк = %d, ждали 2: %+v", len(rows), rows)
	}
	if rows[0].Name != "A_USED" || !rows[0].Used {
		t.Fatalf("первой должна идти используемая переменная: %+v", rows)
	}
	if rows[1].Name != "LEFTOVER" || !rows[1].Set || rows[1].Used {
		t.Fatalf("сохранённая без ссылок: %+v", rows[1])
	}
}

// Ссылка ${X} в сервере помечает строку used:true.
func TestEnvListMarksUsed(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "db", map[string]string{"PASSWORD": "${DB_PASSWORD}"})

	rows := envList(t, e)
	if len(rows) != 1 || rows[0].Name != "DB_PASSWORD" || !rows[0].Used {
		t.Fatalf("ссылка сервера не помечена: %+v", rows)
	}
}

// Имя и из ссылок, и из env.json даёт одну строку, а не две.
func TestEnvListNoDuplicates(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "db", map[string]string{"PASSWORD": "${DB_PASSWORD}"})
	if err := e.m.sec.local.set("DB_PASSWORD", "v"); err != nil {
		t.Fatalf("env.json: %v", err)
	}

	rows := envList(t, e)
	seen := 0
	for _, v := range rows {
		if v.Name == "DB_PASSWORD" {
			seen++
			if !v.Set || !v.Used {
				t.Fatalf("строка потеряла set/used: %+v", v)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("DB_PASSWORD встречается %d раз: %+v", seen, rows)
	}
}
