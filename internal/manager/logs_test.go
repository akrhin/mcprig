package manager

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSetLogsTailFromFile: логи берутся из файла набора, а не из кольца в
// памяти (после рестарта панели кольцо пустое, файл — нет).
func TestSetLogsTailFromFile(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "logs", "fake")
	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	defer e.m.rt.Stop(set)

	// Кольцо чистим: строка обязана прийти из файла.
	r := e.m.rt.ring(id)
	r.mu.Lock()
	r.lines, r.partial = nil, ""
	r.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for {
		w := e.do(t, http.MethodGet, "/api/sets/"+id+"/logs?lines=5", nil)
		status(t, w, http.StatusOK)
		if strings.Contains(w.Body.String(), "fakeset слушает") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("в логах нет хвоста файла: %s", w.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Унаследованный от lazy-mcp файл лога переезжает в mcprig.log при старте
// набора: хвост не пропадает из панели.
func TestSetLogMigratesInheritedFile(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "mig", "fake")
	dir := filepath.Join(e.dir, "sets", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "lazy-mcp.log")
	if err := os.WriteFile(old, []byte("хвост из lazy-mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	defer e.m.rt.Stop(set)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("lazy-mcp.log остался: %v", err)
	}
	if logs := e.m.rt.Logs(id, 500); !strings.Contains(strings.Join(logs, "\n"), "хвост из lazy-mcp") {
		t.Fatalf("унаследованный хвост потерян: %v", logs)
	}
}

// Существующий mcprig.log миграция не затирает: старый файл остаётся на месте.
func TestLogMigrationKeepsExistingFile(t *testing.T) {
	dir := t.TempDir()
	setDir := filepath.Join(dir, "sets", "main")
	if err := os.MkdirAll(setDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(setDir, "lazy-mcp.log")
	next := filepath.Join(setDir, "mcprig.log")
	if err := os.WriteFile(old, []byte("старый\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte("новый\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	migrateLog(nil, dir, "main")

	if b, err := os.ReadFile(next); err != nil || string(b) != "новый\n" {
		t.Fatalf("mcprig.log затёрт: %q, %v", b, err)
	}
	if b, err := os.ReadFile(old); err != nil || string(b) != "старый\n" {
		t.Fatalf("lazy-mcp.log пропал: %q, %v", b, err)
	}
}

// TestLogWriterRotatesBySize: долгоживущий набор не растёт без предела —
// .log переезжает в .log.1 при 5 МиБ.
func TestLogWriterRotatesBySize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcprig.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	w := &logWriter{file: f, path: path, ring: &ring{}}
	chunk := []byte(strings.Repeat("x", 1<<20) + "\n")
	for i := 0; i < 6; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	w.file.Close()

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("нет .log.1 после 6 МиБ: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxLogBytes {
		t.Fatalf("текущий лог %d байт, больше %d", info.Size(), maxLogBytes)
	}
}
