package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
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

// Ответ под лимитом уходит байт-в-байт и не заводит файл в tool-out: иначе
// каждая мелочь оставляла бы мусор и маркер усечения.
func TestUnderLimitAnswerUntouched(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	const fullText = "короткий ответ: 42"
	cs := clientFor(t, managerWithTextTool(t, fullText).Server())

	text := resultText(t, call(t, cs, "invoke_command", map[string]any{"server": "big", "command_name": "big"}))
	if text != fullText {
		t.Fatalf("ответ под лимитом изменён: %q", text)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "tool-out")); err == nil && len(entries) > 0 {
		t.Fatalf("под лимитом создан файл в tool-out: %v", entries)
	}
}

// maxBytes сверх потолка отвергается: иначе «побольше» молча означало бы
// «сколько разрешено» уже после чтения аргументов.
func TestInvokeCommandRejectsOversizeMaxBytes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	cs := clientFor(t, managerWithTextTool(t, "ответ").Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "big", "command_name": "big", "maxBytes": maxAllowedBytes + 1,
	})
	if !res.IsError {
		t.Fatalf("maxBytes сверх потолка принят: %s", resultText(t, res))
	}
	if text := resultText(t, res); !strings.Contains(text, "допустимо не больше 1048576") {
		t.Fatalf("отказ не называет потолок: %q", text)
	}
}

// Усечение structuredContent обязано называть файл: клиент со structured-смыслом
// (ticktick и подобные) иначе получает пустоту и не знает, где искать данные.
// Конверт режется структурно, поэтому остаётся валидным JSON.
func TestTruncatedStructuredAnswerNamesFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	m := New(&config.Config{Servers: []*config.Server{stdio("big")}}, "test", nil)
	m.dial = func(context.Context, *config.Server) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "big", Version: "test"}, nil)
		s.AddTool(&mcp.Tool{Name: "big", InputSchema: objectSchema(map[string]any{})},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{
					Content:           []mcp.Content{&mcp.TextContent{Text: "ticktick"}},
					StructuredContent: map[string]any{"items": strings.Repeat("z", 40000)},
				}, nil
			})
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).
			Connect(context.Background(), ct, nil)
	}
	t.Cleanup(func() { _ = m.Close() })
	cs := clientFor(t, m.Server())

	text := resultText(t, call(t, cs, "invoke_command", map[string]any{"server": "big", "command_name": "big"}))
	var doc struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Items string `json:"items"`
		} `json:"structuredContent"`
		Truncated string `json:"_truncated"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("конверт перестал быть JSON: %v (%q)", err, text)
	}
	if len(doc.Content) != 1 || doc.Content[0].Text != "ticktick" {
		t.Fatalf("усечение потеряло текст конверта: %+v", doc.Content)
	}
	if n := len(doc.StructuredContent.Items); n == 0 || n >= 40000 {
		t.Fatalf("structuredContent не укорочен: %d", n)
	}
	data, err := os.ReadFile(dumpPath(t, doc.Truncated))
	if err != nil {
		t.Fatalf("полный ответ не выгружен: %v", err)
	}
	if !strings.Contains(string(data), strings.Repeat("z", 40000)) {
		t.Fatalf("в файле нет structuredContent: %d байт", len(data))
	}
}

// Длинная строка внутри JSON: ответ остаётся валидным документом, признак
// усечения — в самом документе, полный ответ — в названном файле.
func TestTruncatedJSONStaysValid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	answer := `{"total":1,"items":[{"id":1,"note":"` + strings.Repeat("z", 50000) + `"}]}`
	cs := clientFor(t, managerWithTextTool(t, answer).Server())

	text := resultText(t, call(t, cs, "invoke_command", map[string]any{"server": "big", "command_name": "big"}))
	if len(text) > defaultMaxBytes {
		t.Fatalf("ответ больше лимита: %d байт", len(text))
	}
	var doc struct {
		Total int `json:"total"`
		Items []struct {
			ID   int    `json:"id"`
			Note string `json:"note"`
		} `json:"items"`
		Truncated string `json:"_truncated"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("ответ перестал быть JSON: %v (%q)", err, text)
	}
	if doc.Total != 1 || len(doc.Items) != 1 || doc.Items[0].ID != 1 {
		t.Fatalf("усечение потеряло структуру: %+v", doc)
	}
	if n := len(doc.Items[0].Note); n == 0 || n >= 50000 {
		t.Fatalf("строка не укорочена: %d", n)
	}
	data, err := os.ReadFile(dumpPath(t, doc.Truncated))
	if err != nil {
		t.Fatalf("полный ответ не выгружен: %v", err)
	}
	if !strings.Contains(string(data), strings.Repeat("z", 50000)) {
		t.Fatalf("в файле нет полного ответа: %d байт", len(data))
	}
}

// Не-JSON ответ режется как раньше, но сообщение честно говорит, что он усечён
// и где лежит полный.
func TestTruncatedRawTextNamesFullAnswer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	fullText := strings.Repeat("строка ", 5000)
	cs := clientFor(t, managerWithTextTool(t, fullText).Server())

	text := resultText(t, call(t, cs, "invoke_command", map[string]any{"server": "big", "command_name": "big"}))
	if !strings.Contains(text, "[обрезано: ") || !strings.Contains(text, "полный ответ — ") {
		t.Fatalf("сообщение не говорит про усечение и полный ответ: %q", text)
	}
	path := dumpPath(t, text)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("полный ответ не выгружен: %v", err)
	}
	if string(data) != fullText {
		t.Fatalf("файл не равен полному ответу: %d байт", len(data))
	}
}
