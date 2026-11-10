package mcpserver

import (
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

// Неизвестное имя сверяется с кэшем инструментов до вызова: в апстрим оно не
// уходит, а текст отказа ровно один.
func TestUnknownToolCheckedAgainstCache(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	m.storeTools(m.find("alpha"), []*mcp.Tool{{Name: "echo"}})
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "nope", "parameters": map[string]any{},
	})
	if !res.IsError {
		t.Fatalf("неизвестный инструмент прошёл: %s", resultText(t, res))
	}
	if got, want := resultText(t, res), `Error: неизвестный инструмент "nope" у сервера "alpha"`; got != want {
		t.Fatalf("текст:\n%s\nожидался:\n%s", got, want)
	}
	if got := atomic.LoadInt32(&dials); got != 0 {
		t.Fatalf("неизвестное имя дошло до апстрима: %d соединений", got)
	}
}
