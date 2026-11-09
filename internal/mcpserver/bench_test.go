package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

// busyUpstream — сервер с n инструментами и правдоподобными описаниями:
// профилируем агрегацию, а не сеть.
func busyUpstream(n int) func(context.Context) (*mcp.ClientSession, error) {
	return func(ctx context.Context) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "busy", Version: "test"}, nil)
		desc := strings.Repeat("инструмент, который делает полезное и подробно себя описывает. ", 6)
		for i := 0; i < n; i++ {
			s.AddTool(&mcp.Tool{
				Name:        fmt.Sprintf("tool_%02d", i),
				Description: desc,
				InputSchema: objectSchema(map[string]any{"text": map[string]any{"type": "string"}}),
			}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})
		}
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "bench", Version: "test"}, nil).Connect(context.Background(), ct, nil)
	}
}

func benchManager(servers, tools int) (*Manager, []string) {
	cfg := &config.Config{}
	var names []string
	for i := 0; i < servers; i++ {
		name := fmt.Sprintf("srv%02d", i)
		names = append(names, name)
		cfg.Servers = append(cfg.Servers, &config.Server{Name: name, Command: []string{"x"}})
	}
	m := New(cfg, "bench", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		return busyUpstream(tools)(ctx)
	}
	return m, names
}

func benchCall(b *testing.B, m *Manager, name string, args map[string]any) {
	b.Helper()
	raw, _ := json.Marshal(args)
	for i := 0; i < b.N; i++ {
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: raw}}
		if _, err := m.listCommands(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListCommandsAll(b *testing.B) {
	m, names := benchManager(11, 40)
	defer m.Close()
	benchCall(b, m, "list_commands", map[string]any{"servers": names})
}

func BenchmarkListCommandsOne(b *testing.B) {
	m, names := benchManager(11, 40)
	defer m.Close()
	benchCall(b, m, "list_commands", map[string]any{"servers": names[:1]})
}

func BenchmarkDescribeCommands(b *testing.B) {
	m, names := benchManager(11, 40)
	defer m.Close()
	benchCall(b, m, "describe_commands", map[string]any{"server": names[0], "command_names": []string{"tool_01"}})
}

func BenchmarkInvokeCommand(b *testing.B) {
	m, names := benchManager(11, 3)
	defer m.Close()
	benchCall(b, m, "invoke_command", map[string]any{"server": names[0], "command_name": "tool_00"})
}
