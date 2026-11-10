package mcpserver

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// serverSecrets — секреты сервера: env и заголовки с «секретным» именем.
func serverSecrets(srv *config.Server) []string { return srv.Secrets() }

// scrub заменяет секреты сервера в тексте ответа: апстрим охотно повторяет
// ключ в теле ошибки (tavily присылает его в validation details), а прокси не
// должен передавать это в контекст модели и в счётчики. Структурированный
// ответ не трогаем: секреты приходят текстом, а разбор JSON ради маскировки
// ломал бы форму результата.
func scrubSecrets(res *mcp.CallToolResult, secrets []string) *mcp.CallToolResult {
	if res == nil || len(secrets) == 0 || len(res.Content) == 0 {
		return res
	}
	content := make([]mcp.Content, len(res.Content))
	changed := false
	for i, item := range res.Content {
		text, ok := item.(*mcp.TextContent)
		if !ok {
			content[i] = item
			continue
		}
		masked := text.Text
		for _, secret := range secrets {
			masked = strings.ReplaceAll(masked, secret, "***")
		}
		if masked == text.Text {
			content[i] = item
			continue
		}
		clone := *text
		clone.Text = masked
		content[i] = &clone
		changed = true
	}
	if !changed {
		return res
	}
	out := *res
	out.Content = content
	return &out
}
