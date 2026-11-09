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
)

const (
	// defaultMaxBytes keeps a typical 50–200 KB search answer out of the
	// agent context without asking for a parameter.
	defaultMaxBytes = 32768
	// maxAllowedBytes is the largest cap a caller may ask for.
	maxAllowedBytes = 1048576
)

// toolOutDir is where an oversized answer is written; created on first use.
func toolOutDir() string {
	base := os.Getenv("MCPRIG_DATA_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "tool-out")
}

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
	return &mcp.CallToolResult{
		IsError: res.IsError,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil
}

// payload is the text the caller would see: a single text item as-is, anything
// else as its serialized form so nothing (images, structured content) is lost.
func payload(res *mcp.CallToolResult) ([]byte, error) {
	if len(res.Content) == 1 && res.StructuredContent == nil {
		if t, ok := res.Content[0].(*mcp.TextContent); ok {
			return []byte(t.Text), nil
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
func filterCommands(cmds []commandInfo, prefix string) []commandInfo {
	if prefix == "" {
		return cmds
	}
	prefix = strings.ToLower(prefix)
	out := make([]commandInfo, 0, len(cmds))
	for _, cmd := range cmds {
		if strings.HasPrefix(strings.ToLower(cmd.Name), prefix) {
			out = append(out, cmd)
		}
	}
	return out
}
