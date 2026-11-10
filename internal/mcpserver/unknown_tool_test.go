package mcpserver

import (
	"strings"
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

// Тёплый кэш не мешает известному имени: вызов доходит до апстрима один раз.
func TestKnownToolPassesWarmCache(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	m.storeTools(m.find("alpha"), []*mcp.Tool{{Name: "echo"}})
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "parameters": map[string]any{},
	})
	if res.IsError {
		t.Fatalf("известный инструмент отвергнут: %s", resultText(t, res))
	}
	if text := resultText(t, res); !strings.Contains(text, "echo:") {
		t.Fatalf("ответ не от апстрима: %q", text)
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("соединений %d, ожидалось 1", got)
	}
}

// Правило deny сильнее кэша: имя есть, а вызов всё равно отклонён политикой,
// и текст говорит о правиле, а не о неизвестном инструменте.
func TestDeniedToolWinsOverCache(t *testing.T) {
	var dials int32
	cfg := &config.Config{
		Permissions: &config.Permissions{Rules: []config.Rule{
			{Pattern: "echo", Action: config.PermissionDeny, Message: "нельзя"},
		}},
		Servers: []*config.Server{stdio("alpha")},
	}
	m := testManager(cfg, &dials, 0)
	defer m.Close()
	m.storeTools(m.find("alpha"), []*mcp.Tool{{Name: "echo"}})
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "parameters": map[string]any{},
	})
	if !res.IsError {
		t.Fatalf("запрещённый вызов прошёл: %s", resultText(t, res))
	}
	text := resultText(t, res)
	if !strings.Contains(text, "нельзя") {
		t.Fatalf("отказ не по правилу: %q", text)
	}
	if strings.Contains(text, "неизвестный инструмент") {
		t.Fatalf("отказ подменён кэшем: %q", text)
	}
	if got := atomic.LoadInt32(&dials); got != 0 {
		t.Fatalf("запрещённый вызов дошёл до апстрима: %d соединений", got)
	}
}
