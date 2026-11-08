package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "file.json")

	if err := Write(path, []byte("one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("права: want 0600, got %o", got)
	}

	if err := Write(path, []byte("two")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "two" {
		t.Errorf("содержимое: want two, got %q", body)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("после записи остались лишние файлы: %v", entries)
	}
}
