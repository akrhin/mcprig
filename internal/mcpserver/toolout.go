package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/toolout"
)

const (
	// defaultMaxBytes keeps a typical 50–200 KB search answer out of the
	// agent context without asking for a parameter.
	defaultMaxBytes = 32768
	// maxAllowedBytes is the largest cap a caller may ask for.
	maxAllowedBytes = 1048576
)

// toolOutDir is where an oversized answer is written; created on first use.
func toolOutDir() string { return toolout.Dir(toolout.Base()) }

// truncateResult caps a downstream answer at maxBytes and points at the file
// carrying the full one. An answer that fits is returned untouched: no file,
// no marker.
func truncateResult(res *mcp.CallToolResult, maxBytes int) (*mcp.CallToolResult, error) {
	full, err := payload(res)
	if err != nil {
		return nil, err
	}
	if len(full) <= maxBytes {
		return res, nil
	}
	path, err := writeToolOut(full)
	if err != nil {
		return nil, err
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(full[cut]) {
		cut--
	}
	text := string(full[:cut]) + fmt.Sprintf("\n[обрезано: %d из %d байт, полный ответ — %s]", cut, len(full), path)
	if res.StructuredContent != nil {
		// Без этой строки клиент со structured-смыслом (ticktick и подобные)
		// видит пустой ответ: структура в текстовый срез не попадает.
		text += "\n[structuredContent не влезает в maxBytes: полный ответ в файле]"
	}
	return &mcp.CallToolResult{
		IsError: res.IsError,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil
}

// truncateValue applies the invoke_command cap and hands back what a caller
// would see: the text of a single text item, anything else serialized.
func truncateValue(res *mcp.CallToolResult, maxBytes int) (any, error) {
	cut, err := truncateResult(res, maxBytes)
	if err != nil {
		return nil, err
	}
	if len(cut.Content) == 1 && cut.StructuredContent == nil {
		if t, ok := cut.Content[0].(*mcp.TextContent); ok {
			return t.Text, nil
		}
	}
	return cut, nil
}

// payload is the text the caller would see: a single text item as-is, anything
// else as its serialized form so nothing (images, structured content) is lost.
func payload(res *mcp.CallToolResult) ([]byte, error) {
	// Склеиваем текстовые части: ответ из 123 кусков иначе сериализовался
	// конвертом {"content":[…]}, и срез 32 КБ начинался с JSON внутри JSON.
	if res.StructuredContent == nil && len(res.Content) > 0 {
		parts := make([]string, 0, len(res.Content))
		for _, item := range res.Content {
			t, ok := item.(*mcp.TextContent)
			if !ok {
				parts = nil
				break
			}
			parts = append(parts, t.Text)
		}
		if len(parts) > 0 {
			return []byte(strings.Join(parts, "\n")), nil
		}
	}
	return json.Marshal(res)
}

func writeToolOut(data []byte) (string, error) {
	dir := toolOutDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("усечение: каталог выгрузки: %w", err)
	}
	var name [8]byte
	if _, err := rand.Read(name[:]); err != nil {
		return "", fmt.Errorf("усечение: имя файла: %w", err)
	}
	path := filepath.Join(dir, hex.EncodeToString(name[:])+".txt")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("усечение: запись полного ответа: %w", err)
	}
	return path, nil
}

// filterCommands keeps names carrying prefix; filtering happens on the way out
// so the tool cache and the traversal stay untouched. Empty prefix keeps all.
func filterCommands(cmds []commandInfo, prefix string, namesOnly bool) any {
	out := make([]commandInfo, 0, len(cmds))
	names := make([]string, 0, len(cmds))
	prefix = strings.ToLower(prefix)
	for _, cmd := range cmds {
		if prefix != "" && !strings.HasPrefix(strings.ToLower(cmd.Name), prefix) {
			continue
		}
		out = append(out, cmd)
		names = append(names, cmd.Name)
	}
	// namesOnly — каталог из 440 тулов занимает ~90 КБ; список имён в шесть раз
	// короче, а описание всё равно берётся через describe_commands.
	if namesOnly {
		return names
	}
	return out
}
