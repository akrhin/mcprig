package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

// managerWithTextTool is a downstream whose only tool returns text as-is.
func managerWithTextTool(t *testing.T, text string) *Manager {
	t.Helper()
	m := New(&config.Config{Servers: []*config.Server{stdio("big")}}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "big", Version: "test"}, nil)
		s.AddTool(&mcp.Tool{Name: "big", InputSchema: objectSchema(map[string]any{})},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
			})
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).
			Connect(context.Background(), ct, nil)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// An answer over the cap is cut, marked, and the whole one lands in a 0600
// file the marker names.
func TestInvokeCommandTruncatesOversizeAnswer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	fullText := strings.Repeat("x", 50000)
	cs := clientFor(t, managerWithTextTool(t, fullText).Server())

	res := call(t, cs, "invoke_command", map[string]any{"server": "big", "command_name": "big"})
	text := resultText(t, res)
	const marker = "[обрезано: 32768 из 50000 байт, полный ответ — "
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("нет маркера усечения: %q", text)
	}
	if got := strings.TrimSuffix(text[:i], "\n"); got != fullText[:32768] {
		t.Fatalf("обрезано не по границе: %d байт", len(got))
	}
	if !strings.HasSuffix(text, "]") {
		t.Fatalf("маркер не закрыт: %q", text[i:])
	}
	path := strings.TrimSuffix(text[i+len(marker):], "]")
	if !strings.HasSuffix(path, ".txt") {
		t.Fatalf("маркер не даёт путь к файлу: %q", path)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("путь к файлу не абсолютный: %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("полный ответ не выгружен: %v", err)
	}
	if string(data) != fullText {
		t.Fatalf("файл не равен полному ответу: %d байт", len(data))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("файл не 0600: %v", info.Mode().Perm())
	}
}

// An answer that fits is returned untouched: no marker, no file.
func TestInvokeCommandSmallAnswerUntouched(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	cs := clientFor(t, managerWithTextTool(t, "short").Server())

	res := call(t, cs, "invoke_command", map[string]any{"server": "big", "command_name": "big"})
	if text := resultText(t, res); text != "short" {
		t.Fatalf("малый ответ изменён: %q", text)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "tool-out")); err == nil && len(entries) != 0 {
		t.Fatalf("файл появился под лимитом: %v", entries)
	}

	// An explicit cap above the answer keeps it untouched as well.
	big := strings.Repeat("y", 50000)
	cs = clientFor(t, managerWithTextTool(t, big).Server())
	res = call(t, cs, "invoke_command", map[string]any{
		"server": "big", "command_name": "big", "maxBytes": 100000,
	})
	if text := resultText(t, res); text != big {
		t.Fatalf("ответ под явным лимитом изменён: %d байт", len(text))
	}
}

// maxBytes is validated like the other arguments: only 0..1048576.
func TestInvokeCommandMaxBytesValidation(t *testing.T) {
	cs := clientFor(t, managerWithTextTool(t, "x").Server())

	cases := []struct {
		name string
		arg  any
		want string
	}{
		{"non-numeric", "abc", "maxBytes"},
		{"negative", -1, "maxBytes: не может быть отрицательным"},
		{"too big", 2000000, "maxBytes: допустимо не больше 1048576 байт"},
	}
	for _, tc := range cases {
		res := call(t, cs, "invoke_command", map[string]any{
			"server": "big", "command_name": "big", "maxBytes": tc.arg,
		})
		if !res.IsError {
			t.Fatalf("%s: ожидалась ошибка", tc.name)
		}
		if text := resultText(t, res); !strings.Contains(text, tc.want) {
			t.Fatalf("%s: ошибка не называет причину: %q", tc.name, text)
		}
	}
}
