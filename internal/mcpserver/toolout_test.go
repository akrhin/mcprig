package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/stats"
	"github.com/akrhin/mcprig/internal/toolout"
)

// dumpDir points the dump at a test directory and restores it afterwards.
func dumpDir(t *testing.T, dir string) {
	t.Helper()
	orig := toolOutDir
	toolOutDir = func() string { return dir }
	t.Cleanup(func() { toolOutDir = orig })
}

func textResult(n int) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", n)}},
	}
}

// dumpPath pulls the file path out of the truncation marker.
func dumpPath(t *testing.T, text string) string {
	t.Helper()
	const lead = "полный ответ — "
	i := strings.Index(text, lead)
	if i < 0 {
		t.Fatalf("нет маркера с путём: %q", text)
	}
	rest := text[i+len(lead):]
	j := strings.Index(rest, "]")
	if j < 0 {
		t.Fatalf("маркер не закрыт: %q", text)
	}
	return rest[:j]
}

// The file carries the whole answer even far past the old 4 MiB ceiling: a
// truncated dump would make the marker's "полный ответ" a lie.
func TestTruncateResultWritesWholeAnswer(t *testing.T) {
	dir := t.TempDir()
	dumpDir(t, dir)

	const payload = 6*1024*1024 + 12345
	const tail = "zeta"
	body := strings.Repeat("x", payload-len(tail)) + tail
	res, err := (&Manager{}).truncateResult(context.Background(), &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: body}},
	}, defaultMaxBytes)
	if err != nil {
		t.Fatalf("truncateResult: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	data, err := os.ReadFile(dumpPath(t, text))
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	if len(data) != payload || !strings.HasSuffix(string(data), tail) {
		t.Fatalf("файл %d байт, ожидалось %d с хвостом %q", len(data), payload, tail)
	}
	if strings.Contains(string(data[len(data)-64:]), "файл обрезан") {
		t.Fatalf("в файле остался маркер обрезки")
	}
}

// A directory that cannot be written to leaves no half-written dump behind.
func TestTruncateResultLeavesNoFileOnWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	dumpDir(t, dir)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := (&Manager{}).truncateResult(context.Background(), textResult(defaultMaxBytes+1), defaultMaxBytes); err == nil {
		t.Fatal("ожидалась ошибка записи полного ответа")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("в каталоге остались файлы: %d", len(entries))
	}
}
func TestTruncateResultReportsDumpDirError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("blocker: %v", err)
	}
	dumpDir(t, filepath.Join(blocker, "sub"))

	_, err := (&Manager{}).truncateResult(context.Background(), textResult(defaultMaxBytes+1), defaultMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "каталог выгрузки") {
		t.Fatalf("ожидалась ошибка каталога выгрузки, получено %v", err)
	}
}

// Over HTTP with a key the marker names the /tool-out URL: a path on the
// server's disk is unreachable for the client that was told it.
func TestTruncateMarkerNamesToolOutURL(t *testing.T) {
	dir := t.TempDir()
	dumpDir(t, dir)

	m := &Manager{ToolOutURL: true}
	res, err := m.truncateResult(context.Background(), textResult(defaultMaxBytes+1), defaultMaxBytes)
	if err != nil {
		t.Fatalf("truncateResult: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "/tool-out/") || !strings.Contains(text, "тот же ключ, которым вызван инструмент") {
		t.Fatalf("в маркере нет адреса: %q", text)
	}
	if strings.Contains(text, dir) {
		t.Errorf("в маркере остался путь на диске: %q", text)
	}
	name := dumpPath(t, text)
	if head, _, _ := strings.Cut(name, " "); len(head) != len("/tool-out/")+16 {
		t.Errorf("адрес в маркере: %q", name)
	}
}

// stdio keeps the path: that file is on the caller's own filesystem.
func TestTruncateMarkerKeepsPathWithoutHTTP(t *testing.T) {
	dir := t.TempDir()
	dumpDir(t, dir)

	res, err := (&Manager{}).truncateResult(context.Background(), textResult(defaultMaxBytes+1), defaultMaxBytes)
	if err != nil {
		t.Fatalf("truncateResult: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if strings.Contains(text, "/tool-out/") {
		t.Fatalf("stdio-маркер обещает HTTP-адрес: %q", text)
	}
	if got := dumpPath(t, text); !strings.HasPrefix(got, dir) {
		t.Errorf("путь в маркере %q, ожидался под %q", got, dir)
	}
}

// A dump belongs to the key whose call produced it: the sidecar carries that
// key's fingerprint, so another key cannot read the answer by name alone.
func TestTruncateWritesOwnerFingerprint(t *testing.T) {
	dir := t.TempDir()
	dumpDir(t, dir)

	ctx := stats.WithClientKey(context.Background(), "abc123")
	res, err := (&Manager{}).truncateResult(ctx, textResult(defaultMaxBytes+1), defaultMaxBytes)
	if err != nil {
		t.Fatalf("truncateResult: %v", err)
	}
	path := dumpPath(t, res.Content[0].(*mcp.TextContent).Text)
	owner, err := os.ReadFile(toolout.OwnerPath(path))
	if err != nil {
		t.Fatalf("владелец: %v", err)
	}
	if got := strings.TrimSpace(string(owner)); got != "abc123" {
		t.Fatalf("владелец %q, ожидался отпечаток ключа", got)
	}

	// Без ключа (открытый набор, stdio) владелец — аноним: такой выгрузкой
	// распоряжается тот, у кого ключа тоже нет.
	res, err = (&Manager{}).truncateResult(context.Background(), textResult(defaultMaxBytes+1), defaultMaxBytes)
	if err != nil {
		t.Fatalf("truncateResult без ключа: %v", err)
	}
	path = dumpPath(t, res.Content[0].(*mcp.TextContent).Text)
	owner, err = os.ReadFile(toolout.OwnerPath(path))
	if err != nil {
		t.Fatalf("владелец: %v", err)
	}
	if got := strings.TrimSpace(string(owner)); got != toolout.AnonymousOwner {
		t.Fatalf("владелец %q, ожидался %q", got, toolout.AnonymousOwner)
	}
}

// namesOnly отдаёт имена, а не каталог: 440 тулов arr-status весили ~90 КБ.
func TestFilterCommandsNamesOnly(t *testing.T) {
	cmds := []commandInfo{
		{Name: "get_torrent_list", Description: "long description"},
		{Name: "add_torrent", Description: "long description"},
	}
	got, ok := filterCommands(cmds, "get_", true).([]string)
	if !ok || len(got) != 1 || got[0] != "get_torrent_list" {
		t.Fatalf("namesOnly: %#v", got)
	}
	full, ok := filterCommands(cmds, "", false).([]commandInfo)
	if !ok || len(full) != 2 || full[1].Description == "" {
		t.Fatalf("полный каталог: %#v", full)
	}
}

// The dump file keeps the whole answer: it streams to disk, so there is no
// reason to cut it and promise a "full answer" that is not there.
func TestSpillWriterWritesWholeAnswer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)

	w := &spillWriter{headLimit: 10}
	if _, err := w.Write([]byte(strings.Repeat("a", 100))); err != nil {
		t.Fatalf("write: %v", err)
	}
	path := w.file.Name()
	if err := w.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	if len(data) != 100 || data[0] != 'a' || data[99] != 'a' {
		t.Fatalf("файл не содержит весь ответ: %d байт", len(data))
	}
}
