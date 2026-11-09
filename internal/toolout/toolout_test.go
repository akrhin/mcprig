package toolout

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// put creates a file of n bytes in dir and backdates its mtime by age.
func put(t *testing.T, dir, name string, n int, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFromEnvGarbageKeepsDefaults(t *testing.T) {
	t.Setenv("MCPRIG_TOOLOUT_TTL", "24 часов")
	t.Setenv("MCPRIG_TOOLOUT_MAX", "много")
	cfg, warns := FromEnv()
	if cfg.TTL != DefaultTTL || cfg.Max != DefaultMax {
		t.Fatalf("мусор подменил умолчания: %+v", cfg)
	}
	if len(warns) != 2 || !strings.Contains(warns[0], "MCPRIG_TOOLOUT_TTL") || !strings.Contains(warns[1], "MCPRIG_TOOLOUT_MAX") {
		t.Fatalf("нет внятных предупреждений: %q", warns)
	}
}

func TestFromEnvValues(t *testing.T) {
	t.Setenv("MCPRIG_TOOLOUT_TTL", "90m")
	t.Setenv("MCPRIG_TOOLOUT_MAX", "1MiB")
	cfg, warns := FromEnv()
	if len(warns) != 0 || cfg.TTL != 90*time.Minute || cfg.Max != 1<<20 {
		t.Fatalf("cfg=%+v warns=%q", cfg, warns)
	}
	if _, err := ParseSize("2048"); err != nil {
		t.Fatalf("простое число байт: %v", err)
	}
	if _, err := ParseSize("-5"); err == nil {
		t.Fatal("отрицательный объём принят")
	}
}

func TestSweepRemovesByTTL(t *testing.T) {
	base := t.TempDir()
	dir := Dir(base)
	old := put(t, dir, "old.txt", 10, 25*time.Hour)
	fresh := put(t, dir, "fresh.txt", 10, time.Minute)

	rep, err := Sweep(base, Config{TTL: 24 * time.Hour, Max: 1 << 20}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Removed != 1 || rep.Freed != 10 {
		t.Fatalf("отчёт: %+v", rep)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("старый файл на месте: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("свежий файл удалён: %v", err)
	}
}

// Свежий файл может читаться прямо сейчас: размерный проход его не трогает,
// даже когда каталог всё равно больше лимита.
func TestSweepBySizeKeepsRecent(t *testing.T) {
	base := t.TempDir()
	dir := Dir(base)
	oldA := put(t, dir, "a.txt", 100, 2*time.Hour)
	oldB := put(t, dir, "b.txt", 100, time.Hour)
	young := put(t, dir, "young.txt", 100, time.Minute)

	rep, err := Sweep(base, Config{TTL: 100 * time.Hour, Max: 50}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Removed != 2 {
		t.Fatalf("должны уйти только старые, отчёт: %+v", rep)
	}
	for _, path := range []string{oldA, oldB} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s на месте", path)
		}
	}
	if _, err := os.Stat(young); err != nil {
		t.Fatalf("файл моложе 5 минут удалён: %v", err)
	}
}

func TestSweepCoversSetDirs(t *testing.T) {
	base := t.TempDir()
	setDir := Dir(filepath.Join(base, "sets", "main"))
	old := put(t, setDir, "old.txt", 10, 25*time.Hour)

	if _, err := Sweep(base, Config{TTL: 24 * time.Hour, Max: 1 << 20}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("каталог набора не почищен: %v", err)
	}
}

// Запись и чистка идут одновременно: ни один только что созданный файл не
// пропадает, старый при этом уходит.
func TestSweepWhileWriting(t *testing.T) {
	base := t.TempDir()
	dir := Dir(base)
	stale := put(t, dir, "stale.txt", 200, 2*time.Hour)
	cfg := Config{TTL: 24 * time.Hour, Max: 100}

	const n = 40
	paths := make([]string, n)
	var wg sync.WaitGroup
	var werr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			path := filepath.Join(dir, fmt.Sprintf("new-%02d.txt", i))
			if err := os.WriteFile(path, make([]byte, 50), 0o600); err != nil {
				werr = err
				return
			}
			paths[i] = path
		}
	}()
	for i := 0; i < 20; i++ {
		if _, err := Sweep(base, cfg, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if werr != nil {
		t.Fatalf("запись: %v", werr)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("старый файл не удалён: %v", err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("свежий файл пропал во время записи: %v", err)
		}
	}
}

// Run чистит сразу и живёт до отмены ctx: горутина не утекает.
func TestRunSweepsAndStops(t *testing.T) {
	base := t.TempDir()
	old := put(t, Dir(base), "old.txt", 10, 25*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := Run(ctx, base, Config{TTL: 24 * time.Hour, Max: 1 << 20}, 20*time.Millisecond, slog.New(slog.DiscardHandler))

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(old); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("стартовый проход не удалил старый файл")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("горутина чистки не завершилась после отмены")
	}
}
