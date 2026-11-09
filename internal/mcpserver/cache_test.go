package mcpserver

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

// Список инструментов берётся из кэша: второй list_commands не поднимает новую
// сессию. Переподключение кэш выбрасывает — иначе после рестарта сервера клиент
// видел бы старый набор.
func TestToolsCacheFilledAndDropped(t *testing.T) {
	cfg := &config.Config{Servers: []*config.Server{{Name: "a", Command: []string{"x"}, Enabled: true}}}
	var dials int32
	m := testManager(cfg, &dials, 0)
	defer m.Close()

	raw, _ := json.Marshal(map[string]any{"servers": []string{"a"}})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "list_commands", Arguments: raw}}
	for i := 0; i < 2; i++ {
		if _, err := m.listCommands(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if m.entries[0].cachedTools() == nil {
		t.Fatal("кэш инструментов не заполнен")
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("подключений %d, ожидалось 1", n)
	}

	m.entries[0].dropSession()
	if m.entries[0].cachedTools() != nil {
		t.Fatal("кэш пережил переподключение")
	}
}

// Прогрев на старте наполняет кэш всех включённых серверов, чтобы первый
// клиентский вызов не ждал холодных npx.
func TestWarmFillsCache(t *testing.T) {
	cfg := &config.Config{Servers: []*config.Server{
		{Name: "a", Command: []string{"x"}, Enabled: true},
		{Name: "b", Command: []string{"x"}, Enabled: true},
		{Name: "off", Command: []string{"x"}, Enabled: false},
	}}
	var dials int32
	m := testManager(cfg, &dials, 0)
	defer m.Close()

	m.Warm(context.Background())
	for _, e := range m.entries[:2] {
		if e.cachedTools() == nil {
			t.Fatalf("%s: прогрев не наполнил кэш", e.srv.Name)
		}
	}
	if n := atomic.LoadInt32(&dials); n != 2 {
		t.Fatalf("подключений %d, ожидалось 2 (выключенный сервер не трогаем)", n)
	}
}

// refresh принимается обоими мета-тулами: клиент может попросить свежий список.
func TestRefreshArgumentAccepted(t *testing.T) {
	cfg := &config.Config{Servers: []*config.Server{{Name: "a", Command: []string{"x"}, Enabled: true}}}
	var dials int32
	m := testManager(cfg, &dials, 0)
	defer m.Close()

	list, _ := json.Marshal(map[string]any{"servers": []string{"a"}, "refresh": true})
	if res, err := m.listCommands(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "list_commands", Arguments: list},
	}); err != nil || res.IsError {
		t.Fatalf("list_commands с refresh: %v %v", err, res)
	}
	descr, _ := json.Marshal(map[string]any{"server": "a", "command_names": []string{"echo"}, "refresh": true})
	if res, err := m.describeCommands(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "describe_commands", Arguments: descr},
	}); err != nil || res.IsError {
		t.Fatalf("describe_commands с refresh: %v %v", err, res)
	}
}
