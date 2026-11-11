package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
// A variable so a test can point the dump at a directory of its own.
var toolOutDir = func() string { return toolout.Dir(toolout.Base()) }

// truncateResult caps a downstream answer at maxBytes and points at the file
// carrying the full one. An answer that fits is returned untouched: no file,
// no marker. Nothing is buffered whole: the head that the caller sees is kept
// in memory, the rest streams to the file.
func truncateResult(res *mcp.CallToolResult, maxBytes int) (*mcp.CallToolResult, error) {
	// headLimit carries maxBytes plus one rune of slack: the cut may land
	// inside a multi-byte rune and the check needs the byte after it.
	w := &spillWriter{headLimit: maxBytes + utf8.UTFMax, capBytes: int64(maxAllowedBytes) * 4}
	if err := writePayload(w, res); err != nil {
		w.abort()
		return nil, err
	}
	if w.total <= int64(maxBytes) {
		return res, nil
	}
	if err := w.ensureFile(); err != nil {
		w.abort()
		return nil, err
	}
	path := w.file.Name()
	if err := w.finish(); err != nil {
		return nil, err
	}
	cut := maxBytes
	if cut > len(w.head) {
		cut = len(w.head)
	}
	for cut > 0 && cut < len(w.head) && !utf8.RuneStart(w.head[cut]) {
		cut--
	}
	text := string(w.head[:cut]) + fmt.Sprintf("\n[обрезано: %d из %d байт, полный ответ — %s]", cut, w.total, path)
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

// writePayload streams what a caller would see: text items joined by a newline,
// or the serialized result when anything else (images, structured content) is
// at hand. Nothing is joined into one big string, so a huge answer costs no
// extra copy in memory.
func writePayload(w io.Writer, res *mcp.CallToolResult) error {
	// Склеиваем текстовые части: ответ из 123 кусков иначе сериализовался
	// конвертом {"content":[…]}, и срез 32 КБ начинался с JSON внутри JSON.
	if res.StructuredContent == nil && len(res.Content) > 0 {
		texts := make([]*mcp.TextContent, 0, len(res.Content))
		for _, item := range res.Content {
			t, ok := item.(*mcp.TextContent)
			if !ok {
				texts = nil
				break
			}
			texts = append(texts, t)
		}
		if len(texts) > 0 {
			for i, t := range texts {
				if i > 0 {
					if _, err := io.WriteString(w, "\n"); err != nil {
						return err
					}
				}
				if _, err := io.WriteString(w, t.Text); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return json.NewEncoder(w).Encode(res)
}

// spillWriter keeps the first headLimit bytes in memory and streams the rest to
// a file created on first overflow, so an answer under the cap never touches
// the disk. capBytes bounds the file: past it bytes are counted, not written.
type spillWriter struct {
	head      []byte
	headLimit int
	capBytes  int64
	file      *os.File
	total     int64 // bytes offered by the source
	kept      int64 // bytes accepted by the file
	capped    bool
}

func (w *spillWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.total += int64(n)
	if room := w.headLimit - len(w.head); room > 0 {
		take := min(room, len(p))
		w.head = append(w.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 {
		return n, nil
	}
	if err := w.ensureFile(); err != nil {
		return 0, err
	}
	if w.kept >= w.capBytes {
		w.capped = true
		return n, nil
	}
	if room := w.capBytes - w.kept; int64(len(p)) > room {
		p = p[:room]
		w.capped = true
	}
	if _, err := w.file.Write(p); err != nil {
		return 0, err
	}
	w.kept += int64(len(p))
	return n, nil
}

// ensureFile creates the dump file on first overflow and writes the head into
// it, so the file starts with the same bytes the caller keeps.
func (w *spillWriter) ensureFile() error {
	if w.file != nil {
		return nil
	}
	f, err := createToolOut()
	if err != nil {
		return err
	}
	w.file = f
	if _, err := f.Write(w.head); err != nil {
		return err
	}
	w.kept = int64(len(w.head))
	return nil
}

// finish closes the file, marking a capped one so a reader knows it is short.
func (w *spillWriter) finish() error {
	if w.file == nil {
		return nil
	}
	if w.capped {
		if _, err := fmt.Fprintf(w.file, "\n[файл обрезан: %d из %d байт]", w.kept, w.total); err != nil {
			return fmt.Errorf("усечение: запись полного ответа: %w", err)
		}
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("усечение: запись полного ответа: %w", err)
	}
	return nil
}

func (w *spillWriter) abort() {
	if w.file != nil {
		_ = w.file.Close()
		_ = os.Remove(w.file.Name())
	}
}

func createToolOut() (*os.File, error) {
	dir := toolOutDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("усечение: каталог выгрузки: %w", err)
	}
	var name [8]byte
	if _, err := rand.Read(name[:]); err != nil {
		return nil, fmt.Errorf("усечение: имя файла: %w", err)
	}
	path := filepath.Join(dir, hex.EncodeToString(name[:])+".txt")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("усечение: запись полного ответа: %w", err)
	}
	return f, nil
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
