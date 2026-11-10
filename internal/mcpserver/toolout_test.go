package mcpserver

import (
	"os"
	"strings"
	"testing"
)

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
