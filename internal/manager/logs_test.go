package manager

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

// TestServerLogsShowProcessOutput: панель отдаёт то, что процесс сервера
// напечатал в stderr, — до этого диалог лога сервера всегда показывал 0 строк.
func TestServerLogsShowProcessOutput(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "sl", "fake")
	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}

	// Так же, как это делает процесс набора: serverlogs/<имя>.log в каталоге набора.
	path := ServerLogPath(e.dir, id, "fake")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("первая строка\nвывод процесса: готов\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := e.do(t, http.MethodGet, "/api/sets/"+id+"/servers/fake/logs?lines=10", nil)
	status(t, w, http.StatusOK)
	got := decode[struct {
		Logs []string `json:"logs"`
	}](t, w).Logs
	if len(got) != 2 || got[1] != "вывод процесса: готов" {
		t.Fatalf("лог сервера = %q", got)
	}

	e.m.rt.Stop(set)

	w = e.do(t, http.MethodGet, "/api/sets/"+id+"/servers/fake/logs?lines=10", nil)
	status(t, w, http.StatusOK)
	if after := decode[struct {
		Logs []string `json:"logs"`
	}](t, w).Logs; len(after) != 0 {
		t.Fatalf("после остановки остался вывод прошлого запуска: %q", after)
	}
}

// tailFile отбрасывает первую строку окна: она прочитана с середины файла и
// целой строкой лога не является.
func TestTailFileDropsPartialFirstLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	lines := []string{"HEAD-" + strings.Repeat("x", 40000)}
	for i := 0; i < 4; i++ {
		lines = append(lines, "строка-"+strconv.Itoa(i)+strings.Repeat("y", 10000))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := tailFile(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != lines[3] || got[1] != lines[4] {
		t.Fatalf("хвост = %q", got)
	}
	if strings.Contains(strings.Join(got, ""), "HEAD-") {
		t.Fatal("в хвост попала обрезанная первая строка")
	}
}
