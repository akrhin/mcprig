package mcpserver

// Tool-out has two homes: the cut of an oversized answer is this file, while
// retention of those files (TTL, size cap, `.owner` sidecars) is
// `internal/toolout`. The names coincide on purpose — both are about the
// tool-out directory; renaming the package would touch 9 imports, 24 call sites
// and 34 doc mentions for no behaviour change.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
	"github.com/akrhin/mcprig/internal/toolout"
)

// dumpOwner is the caller's key fingerprint, the same value the counters use.
func dumpOwner(ctx context.Context) string {
	if fp := stats.ClientKey(ctx); fp != "" {
		return fp
	}
	return toolout.AnonymousOwner
}

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

// maxStructBytes bounds the structural cut: a JSON document is parsed whole, so
// an answer above this is cut raw instead of costing that much memory.
const maxStructBytes = 8 << 20

// truncateResult caps a downstream answer at maxBytes and points at the full
// one. An answer that fits is returned untouched: no file, no marker. The head
// that the caller sees is kept in memory, the rest streams to the file. A JSON
// answer is cut structurally, so it stays valid JSON; anything else gets a
// trailing marker. The marker names the file by path, or by its /tool-out URL
// when the manager serves HTTP with a key. The dump remembers the caller's key
// fingerprint, so another key cannot read it by name alone.
func (m *Manager) truncateResult(ctx context.Context, res *mcp.CallToolResult, maxBytes int) (*mcp.CallToolResult, error) {
	// headLimit carries maxBytes plus one rune of slack: the cut may land
	// inside a multi-byte rune and the check needs the byte after it.
	w := &spillWriter{headLimit: maxBytes + utf8.UTFMax, owner: dumpOwner(ctx)}
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
	ref := m.toolOutRef(path)
	mark := fmt.Sprintf("ответ усечён: %d байт, полный ответ — %s", w.total, ref)
	if w.total <= maxStructBytes {
		if raw, err := os.ReadFile(path); err == nil {
			if text, ok := truncateJSON(raw, maxBytes, mark); ok {
				return &mcp.CallToolResult{
					IsError: res.IsError,
					Content: []mcp.Content{&mcp.TextContent{Text: text}},
				}, nil
			}
		}
	}
	cut := maxBytes
	if cut > len(w.head) {
		cut = len(w.head)
	}
	for cut > 0 && cut < len(w.head) && !utf8.RuneStart(w.head[cut]) {
		cut--
	}
	text := string(w.head[:cut]) + fmt.Sprintf("\n[обрезано: %d из %d байт, полный ответ — %s]", cut, w.total, ref)
	if res.StructuredContent != nil {
		// Без этой строки клиент со structured-смыслом (ticktick и подобные)
		// видит пустой ответ: структура в текстовый срез не попадает.
		text += "\n[structuredContent больше maxBytes: полный ответ в файле]"
	}
	return &mcp.CallToolResult{
		IsError: res.IsError,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil
}

// truncateJSON keeps the answer valid JSON: it shortens string values until the
// document fits maxBytes and records the cut inside the document — a marker
// appended after JSON text would break it. ok is false when the payload is not
// one JSON document, and the caller falls back to a raw cut.
func truncateJSON(raw []byte, maxBytes int, mark string) (string, bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	// Место под признак усечения: ключ, кавычки, запятая.
	target := maxBytes - len(mark) - 24
	if target <= 0 {
		return "", false
	}
	for range 32 {
		out, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		over := len(out) - target
		if over <= 0 {
			break
		}
		if !shrinkStrings(&v, over) {
			arr, ok := v.([]any)
			if !ok || len(arr) < 2 {
				break
			}
			// Документ без строк — массив: срезаем хвост.
			v = arr[:len(arr)/2]
		}
	}
	marked, ok := withMark(v, mark)
	if !ok {
		return "", false
	}
	out, err := json.Marshal(marked)
	if err != nil || len(out) > maxBytes {
		return "", false
	}
	return string(out), true
}

// stringFloor keeps short values (ids, paths, names) intact while the long
// fields shrink: cutting every string by a share mangles identifiers and makes
// the answer useless in a different way than a raw cut.
const stringFloor = 32

// shrinkStrings cuts at least over bytes off the document's longest strings,
// longest first. False — nothing left above the floor, yet the answer is still
// too big (shortcut: many medium strings fall back to the raw cut).
func shrinkStrings(v *any, over int) bool {
	var lens []int
	eachString(*v, func(s string) {
		if len(s) > stringFloor {
			lens = append(lens, len(s))
		}
	})
	if len(lens) == 0 {
		return false
	}
	sort.Sort(sort.Reverse(sort.IntSlice(lens)))
	// Самый высокий порог, который освобождает over байт: чем он выше, тем
	// меньше строк укорочено.
	keep := stringFloor
	prefix := 0
	for j, n := range lens {
		prefix += n
		cand := (prefix - over) / (j + 1)
		if cand < stringFloor {
			cand = stringFloor
		}
		keep = cand
		if cand <= n {
			break
		}
	}
	*v = mapStrings(*v, func(s string) string {
		if len(s) <= keep {
			return s
		}
		return cutRunes(s, keep)
	})
	return true
}

func eachString(v any, f func(string)) {
	switch t := v.(type) {
	case string:
		f(t)
	case map[string]any:
		for _, item := range t {
			eachString(item, f)
		}
	case []any:
		for _, item := range t {
			eachString(item, f)
		}
	}
}

func mapStrings(v any, f func(string) string) any {
	switch t := v.(type) {
	case string:
		return f(t)
	case map[string]any:
		for k, item := range t {
			t[k] = mapStrings(item, f)
		}
		return t
	case []any:
		for i, item := range t {
			t[i] = mapStrings(item, f)
		}
		return t
	}
	return v
}

// cutRunes keeps the first n bytes of s without splitting a rune.
func cutRunes(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// withMark puts the cut into the document itself: an object gains the key, an
// array an element. A scalar has nowhere to put it.
func withMark(v any, mark string) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		t["_truncated"] = mark
		return t, true
	case []any:
		return append(t, mark), true
	}
	return nil, false
}

// toolOutRef names the dump the way the caller can actually reach it: the
// on-disk path is useless to an HTTP client, while /tool-out answers under the
// same guard as /mcp. stdio keeps the path — its file is on the caller's own
// filesystem.
func (m *Manager) toolOutRef(path string) string {
	if !m.ToolOutURL {
		return path
	}
	name := strings.TrimSuffix(filepath.Base(path), ".txt")
	return "/tool-out/" + name + " (тот же ключ, которым вызван инструмент)"
}

// truncateValue applies the invoke_command cap and hands back what a caller
// would see: the text of a single text item, anything else serialized.
func (m *Manager) truncateValue(ctx context.Context, res *mcp.CallToolResult, maxBytes int) (any, error) {
	cut, err := m.truncateResult(ctx, res, maxBytes)
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
// the disk. The file keeps the whole answer: it streams, so its size costs no
// memory.
type spillWriter struct {
	head      []byte
	headLimit int
	owner     string // fingerprint of the calling key, written beside the dump
	file      *os.File
	total     int64 // bytes offered by the source
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
	if _, err := w.file.Write(p); err != nil {
		return 0, err
	}
	return n, nil
}

// ensureFile creates the dump file on first overflow and writes the head into
// it, so the file starts with the same bytes the caller keeps.
func (w *spillWriter) ensureFile() error {
	if w.file != nil {
		return nil
	}
	f, err := createToolOut(w.owner)
	if err != nil {
		return err
	}
	w.file = f
	if _, err := f.Write(w.head); err != nil {
		return err
	}
	return nil
}

// finish closes the file. A failure leaves no half-written dump behind: the
// caller gets the error and the file is removed.
func (w *spillWriter) finish() error {
	if w.file == nil {
		return nil
	}
	if err := w.file.Close(); err != nil {
		w.abort()
		return fmt.Errorf("усечение: запись полного ответа: %w", err)
	}
	return nil
}

func (w *spillWriter) abort() {
	if w.file != nil {
		_ = w.file.Close()
		_ = os.Remove(w.file.Name())
		_ = os.Remove(toolout.OwnerPath(w.file.Name()))
	}
}

func createToolOut(owner string) (*os.File, error) {
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
	// Владелец пишется до тела: без него выгрузка не отдаётся никому, и
	// недописанная пара не должна остаться на диске.
	if err := os.WriteFile(toolout.OwnerPath(path), []byte(owner+"\n"), 0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("усечение: владелец выгрузки: %w", err)
	}
	return f, nil
}

// profileCommands keeps only what the caller's profile allows. The profile is
// read once per list_commands: one key has one profile.
func profileCommands(name string, profile *config.Profile, cmds []commandInfo) []commandInfo {
	if name == "" {
		return cmds
	}
	out := make([]commandInfo, 0, len(cmds))
	for _, cmd := range cmds {
		// nil-профиль значит, что блока с таким именем нет в наборе: ключ
		// остаётся запрещённым, а не получает всё.
		if profile != nil && profile.Allows(cmd.Server, cmd.Name) {
			out = append(out, cmd)
		}
	}
	return out
}

// profileTools keeps only the tools the caller's profile allows, under the same
// rule as profileCommands: describe_commands must not hand out a schema for a
// tool the catalog already hides from this key.
func profileTools(name string, profile *config.Profile, server string, tools []*mcp.Tool) []*mcp.Tool {
	if name == "" {
		return tools
	}
	cmds := make([]commandInfo, 0, len(tools))
	for _, t := range tools {
		cmds = append(cmds, commandInfo{Server: server, Name: t.Name})
	}
	kept := make(map[string]bool, len(cmds))
	for _, c := range profileCommands(name, profile, cmds) {
		kept[c.Name] = true
	}
	out := make([]*mcp.Tool, 0, len(kept))
	for _, t := range tools {
		if kept[t.Name] {
			out = append(out, t)
		}
	}
	return out
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
