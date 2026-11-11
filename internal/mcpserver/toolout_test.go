package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// The 4 MiB file ceiling is real: an answer past it is cut on disk too, not
// only in the text the caller sees.
func TestTruncateResultCapsFourMiB(t *testing.T) {
	dumpDir(t, t.TempDir())

	const fileCap = maxAllowedBytes * 4
	res, err := truncateResult(textResult(fileCap+64*1024), maxAllowedBytes)
	if err != nil {
		t.Fatalf("truncateResult: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	data, err := os.ReadFile(dumpPath(t, text))
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	const marker = "\n[файл обрезан: "
	i := strings.Index(string(data), marker)
	if i < 0 || !strings.HasSuffix(string(data), " байт]") {
		t.Fatalf("файл не помечен как обрезанный: %d байт", len(data))
	}
	if kept := int64(i); kept != int64(fileCap) {
		t.Fatalf("в файл попало %d байт, потолок %d", kept, fileCap)
	}
}

// A dump directory that cannot be created is an error, not a silent cut: the
// caller must not think the full answer is on disk.
func TestTruncateResultReportsDumpDirError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("blocker: %v", err)
	}
	dumpDir(t, filepath.Join(blocker, "sub"))

	_, err := truncateResult(textResult(defaultMaxBytes+1), defaultMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "каталог выгрузки") {
		t.Fatalf("ожидалась ошибка каталога выгрузки, получено %v", err)
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

// The dump file is capped: past capBytes nothing more is written, and the file
// says so, otherwise a reader takes a cut dump for the whole answer.
func TestSpillWriterCapsAndMarks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)

	w := &spillWriter{headLimit: 10, capBytes: 16}
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
	const marker = "[файл обрезан: 16 из 100 байт]"
	if !strings.HasSuffix(string(data), marker) {
		t.Fatalf("нет отметки об обрезке файла: %q", data)
	}
	if kept := strings.TrimSuffix(string(data), "\n"+marker); len(kept) != 16 {
		t.Fatalf("в файл попало %d байт, потолок 16", len(kept))
	}
}
