package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sintez/mcprig/internal/config"
)

// Один упавший сервер в списке не скрывает ответы остальных.
func TestListCommandsPartialFailure(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "list_commands", map[string]any{"servers": []string{"alpha", "nope"}})
	if res.IsError {
		t.Fatalf("упал один — ошибка не должна скрывать ответ остальных: %s", resultText(t, res))
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("ответ не карта: %v (%s)", err, resultText(t, res))
	}
	var cmds []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out["alpha"], &cmds); err != nil || len(cmds) != 3 {
		t.Fatalf("alpha: %v, %s", err, out["alpha"])
	}
	var bad struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out["nope"], &bad); err != nil || !strings.Contains(bad.Error, "неизвестный сервер") {
		t.Fatalf("nope: %v, %s", err, out["nope"])
	}

	all := call(t, cs, "list_commands", map[string]any{"servers": []string{"nope", "gone"}})
	if !all.IsError {
		t.Fatalf("все упали — ожидалась ошибка, получили %s", resultText(t, all))
	}
}

// prefix keeps only names starting with it, in both forms, without touching
// the cache used by later calls.
func TestListCommandsPrefixFilter(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha"), stdio("beta")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	single := call(t, cs, "list_commands", map[string]any{"server": "alpha", "prefix": "DA"})
	var one []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(resultText(t, single)), &one); err != nil {
		t.Fatalf("ответ не массив: %v", err)
	}
	if len(one) != 1 || one[0].Name != "danger-zone" {
		t.Fatalf("prefix case-insensitive: got %+v", one)
	}

	multi := call(t, cs, "list_commands", map[string]any{
		"servers": []string{"alpha", "beta"}, "prefix": "ec",
	})
	var many map[string][]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(resultText(t, multi)), &many); err != nil {
		t.Fatalf("ответ не карта: %v", err)
	}
	for _, server := range []string{"alpha", "beta"} {
		if len(many[server]) != 1 || many[server][0].Name != "echo" {
			t.Fatalf("%s: prefix не отфильтровал: %+v", server, many[server])
		}
	}

	if empty := call(t, cs, "list_commands", map[string]any{"server": "alpha", "prefix": "zzz"}); empty.IsError ||
		strings.TrimSpace(resultText(t, empty)) != "[]" {
		t.Fatalf("пустой фильтр должен дать [], получили %s", resultText(t, empty))
	}

	unfiltered := call(t, cs, "list_commands", map[string]any{"server": "alpha"})
	var all []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(resultText(t, unfiltered)), &all); err != nil || len(all) != 3 {
		t.Fatalf("кэш схем испорчен фильтром: %v, %s", err, resultText(t, unfiltered))
	}
}
