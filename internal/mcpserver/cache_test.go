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
