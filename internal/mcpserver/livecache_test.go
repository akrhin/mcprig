package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/downstream"
)

// A downstream that gains a tool on a live session sends
// notifications/tools/list_changed; the set drops its cached list, so the next
// list_commands sees the new tool without refresh: true.
func TestToolsListChangedDropsCache(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "down", Version: "test"}, nil)
	add := func(name string) {
		s.AddTool(&mcp.Tool{Name: name, InputSchema: objectSchema(map[string]any{})},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name}}}, nil
			})
	}
	add("before")

	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	defer ts.Close()

	cfg := &config.Config{Servers: []*config.Server{{
		Name: "down", Transport: config.TransportHTTP, URL: ts.URL, Enabled: true,
	}}}
	// The real connector: it wires the notification handler New installs.
	m := New(cfg, "test", &downstream.Connector{Version: "test"})
	defer m.Close()

	names := func() []string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"server": "down"})
		res, err := m.listCommands(context.Background(), &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Name: "list_commands", Arguments: raw},
		})
		if err != nil || res.IsError {
			t.Fatalf("list_commands: %v %v", err, res)
		}
		var out []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatalf("list_commands output: %v", err)
		}
		got := make([]string, len(out))
		for i, c := range out {
			got[i] = c.Name
		}
		return got
	}

	if got := names(); len(got) != 1 || got[0] != "before" {
		t.Fatalf("want [before], got %v", got)
	}
	if m.entries[0].cachedTools() == nil {
		t.Fatal("кэш инструментов не заполнен")
	}

	add("after")
	deadline := time.Now().Add(5 * time.Second)
	for m.entries[0].cachedTools() != nil {
		if time.Now().After(deadline) {
			t.Fatal("уведомление list_changed не сбросило кэш")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := names(); len(got) != 2 {
		t.Fatalf("без refresh должен быть виден новый инструмент, got %v", got)
	}
}
