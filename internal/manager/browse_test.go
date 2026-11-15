package manager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// Логирование в просмотре набора показывается по факту: инструмент
// logging/setLevel в списке, а не deprecated-capability в handshake.
func TestBrowseLoggingFromToolList(t *testing.T) {
	tools := []browseTool{{Name: "echo"}, {Name: loggingToolName}}
	if !offersLogging(tools) {
		t.Fatal("logging/setLevel в списке, а logging не показан")
	}
	if offersLogging([]browseTool{{Name: "echo"}, {Name: "fetch"}}) {
		t.Fatal("logging показан без инструмента")
	}
	if offersLogging(nil) {
		t.Fatal("пустой список дал logging")
	}
}

// browseCallFixture открывает настоящую MCP-сессию в памяти: два инструмента,
// правила передаются как у сервера реестра.
func browseCallFixture(t *testing.T, perms *config.Permissions) *browser {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0"}, nil)
	for _, name := range []string{"read_all", "danger_rm"} {
		srv.AddTool(
			&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name + " ok"}}}, nil
			},
		)
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	b := newBrowser(nil, "test", "")
	b.items["s1"] = &browseSession{view: &browseView{ID: "s1"}, sess: sess, used: time.Now(), server: "alpha", perms: perms}
	return b
}

// Вызов инструмента в сессии просмотра подчиняется `permissions` сервера так
// же, как invoke_command: запрещённый отвергается до обращения к серверу,
// разрешённый проходит.
func TestBrowseCallToolHonoursPermissions(t *testing.T) {
	perms := &config.Permissions{Rules: []config.Rule{
		{Pattern: "danger_*", Action: config.PermissionDeny, Message: "только чтение"},
	}}
	b := browseCallFixture(t, perms)

	_, err := b.callTool(context.Background(), "s1", "danger_rm", nil)
	if !errors.Is(err, errBrowseDenied) {
		t.Fatalf("запрещённый инструмент не отвергнут: %v", err)
	}
	if !strings.Contains(err.Error(), "только чтение") {
		t.Fatalf("в ошибке нет текста правила: %q", err)
	}

	res, err := b.callTool(context.Background(), "s1", "read_all", nil)
	if err != nil {
		t.Fatalf("разрешённый инструмент отвергнут: %v", err)
	}
	out, ok := res.(*mcp.CallToolResult)
	if !ok || len(out.Content) == 0 {
		t.Fatalf("результат не CallToolResult: %T", res)
	}
	if text, _ := out.Content[0].(*mcp.TextContent); text == nil || text.Text != "read_all ok" {
		t.Fatalf("результат = %v", out.Content[0])
	}
}

// Запрет по default без сообщения отдаёт текст sentinel'а; пустая политика
// ничего не запрещает.
func TestBrowseDenyToolMessage(t *testing.T) {
	s := &browseSession{server: "alpha", perms: &config.Permissions{Default: config.PermissionDeny}}
	err := s.deny("anything")
	if !errors.Is(err, errBrowseDenied) || err.Error() != errBrowseDenied.Error() {
		t.Fatalf("deny = %v", err)
	}
	if err := (&browseSession{server: "alpha"}).deny("anything"); err != nil {
		t.Fatalf("без политики инструмент отвергнут: %v", err)
	}
}
