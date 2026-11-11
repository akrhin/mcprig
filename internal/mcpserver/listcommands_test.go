package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
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

// A catalog over maxBytes is cut like any other answer, and the whole one lands
// in a file the marker names.
func TestListCommandsTruncatesHugeCatalog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	m := New(&config.Config{Servers: []*config.Server{stdio("wide")}}, "test", nil)
	m.dial = func(context.Context, *config.Server) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "wide", Version: "test"}, nil)
		for i := range 40 {
			s.AddTool(&mcp.Tool{
				Name:        fmt.Sprintf("tool-%02d", i),
				Description: strings.Repeat("d", 1000),
				InputSchema: objectSchema(map[string]any{}),
			}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})
		}
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).
			Connect(context.Background(), ct, nil)
	}
	t.Cleanup(func() { _ = m.Close() })
	cs := clientFor(t, m.Server())

	// Without maxBytes the catalog arrives whole: the old contract, untouched.
	whole := resultText(t, call(t, cs, "list_commands", map[string]any{"server": "wide"}))
	var catalog []any
	if err := json.Unmarshal([]byte(whole), &catalog); err != nil {
		t.Fatalf("без maxBytes каталог должен быть целым JSON: %v", err)
	}
	if len(catalog) != 40 {
		t.Fatalf("без maxBytes в каталоге %d команд, ожидалось 40", len(catalog))
	}
	if !strings.Contains(whole, "tool-39") || strings.Contains(whole, "[обрезано: ") {
		t.Fatalf("без maxBytes каталог обрезан")
	}

	text := resultText(t, call(t, cs, "list_commands", map[string]any{"server": "wide", "maxBytes": 2000}))
	const cutMark = "[обрезано: "
	i := strings.Index(text, cutMark)
	if i < 0 {
		t.Fatalf("нет маркера усечения: %q", text)
	}
	rest := text[strings.Index(text, "полный ответ — ")+len("полный ответ — "):]
	path := rest[:strings.Index(rest, "]")]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("полный каталог не выгружен: %v", err)
	}
	if len(data) < 2000 || !strings.Contains(string(data), "tool-39") {
		t.Fatalf("файл не содержит полный каталог: %d байт", len(data))
	}

	// describe_commands takes the same cap.
	described := resultText(t, call(t, cs, "describe_commands", map[string]any{
		"server": "wide", "command_names": []string{"tool-00", "tool-01"}, "maxBytes": 100,
	}))
	if !strings.Contains(described, "[обрезано: 100 из ") {
		t.Fatalf("describe_commands не режет: %q", described)
	}
}
