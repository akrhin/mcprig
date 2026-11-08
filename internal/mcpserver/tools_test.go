package mcpserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

// requestTimeout must bound invoke_command: a slow downstream is cut off with a
// message naming the configured limit.
func TestInvokeCommandTimeout(t *testing.T) {
	m := New(&config.Config{
		RequestTimeout: 20,
		Servers:        []*config.Server{stdio("slow")},
	}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "slow", Version: "test"}, nil)
		s.AddTool(&mcp.Tool{Name: "wait", InputSchema: objectSchema(map[string]any{})},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				time.Sleep(2 * time.Second)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil
			})
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).
			Connect(context.Background(), ct, nil)
	}
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{"server": "slow", "command_name": "wait"})
	if !res.IsError {
		t.Fatal("медленный вызов должен истечь по таймауту")
	}
	if text := resultText(t, res); !strings.Contains(text, "таймаут вызова 20 мс") {
		t.Fatalf("want a timeout naming 20 мс, got %q", text)
	}
}

// describe_commands passes the downstream input schema through untouched.
func TestDescribeCommandsKeepsSchema(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "describe_commands", map[string]any{
		"server": "alpha", "command_names": []string{"echo"},
	})
	if res.IsError {
		t.Fatalf("describe_commands: %s", resultText(t, res))
	}
	var out struct {
		Commands []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("describe_commands output: %v", err)
	}
	if len(out.Commands) != 1 || out.Commands[0].Name != "echo" {
		t.Fatalf("want the echo schema, got %+v", out.Commands)
	}
	wantJSON, err := json.Marshal(objectSchema(map[string]any{"text": map[string]any{"type": "string"}}))
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(out.Commands[0].InputSchema, &got); err != nil {
		t.Fatalf("inputSchema не JSON: %v", err)
	}
	if err := json.Unmarshal(wantJSON, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("схема искажена: got %s", out.Commands[0].InputSchema)
	}
}

// The parameter is command_names; the plural `commands` is refused instead of
// being silently ignored.
func TestDescribeCommandsParameterName(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "describe_commands", map[string]any{
		"server": "alpha", "commands": []string{"echo"},
	})
	if !res.IsError {
		t.Fatal("commands вместо command_names должен быть ошибкой")
	}
	if text := resultText(t, res); !strings.Contains(text, "commands") {
		t.Fatalf("ошибка не называет лишний ключ: %q", text)
	}
}

// An unknown server name is an error for both tools; a missing command name is
// reported in `missing` (see TestDescribeCommandsSkipsMissing).
func TestUnknownServerIsError(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	cases := []struct {
		name string
		args map[string]any
	}{
		{"list_commands", map[string]any{"server": "nope"}},
		{"describe_commands", map[string]any{"server": "nope", "command_names": []string{"echo"}}},
	}
	for _, tc := range cases {
		res := call(t, cs, tc.name, tc.args)
		if !res.IsError {
			t.Fatalf("%s: неизвестный сервер должен быть ошибкой", tc.name)
		}
		if text := resultText(t, res); !strings.Contains(text, "неизвестный сервер") {
			t.Errorf("%s: ошибка не называет причину: %q", tc.name, text)
		}
	}
}
