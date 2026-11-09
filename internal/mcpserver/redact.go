package mcpserver

import (
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

// secretKey опознаёт поля конфигурации, в которых лежит секрет: ключ, токен,
// пароль, заголовок авторизации.
var secretKey = regexp.MustCompile(`(?i)(key|token|secret|pass|auth|credential)`)

// serverSecrets собирает значения, которые нельзя выпускать наружу: env и
// заголовки сервера с «секретным» именем. Значение короче 8 символов не
// маскируется — иначе случайное совпадение испортит обычный текст ответа.
func serverSecrets(srv *config.Server) []string {
	out := make([]string, 0, len(srv.Env)+len(srv.Headers))
	add := func(name, value string) {
		if !secretKey.MatchString(name) {
			return
		}
		value = strings.TrimSpace(value)
		if low := strings.ToLower(value); strings.HasPrefix(low, "bearer ") {
			value = strings.TrimSpace(value[len("bearer "):])
		}
		if len(value) < 8 {
			return
		}
		out = append(out, value)
	}
	for name, value := range srv.Env {
		add(name, value)
	}
	for name, value := range srv.Headers {
		add(name, value)
	}
	return out
}

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
