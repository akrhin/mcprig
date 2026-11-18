package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Повторный запрос по неизменившимся файлам читает каждый из них один раз:
// панель опрашивает /api/stats, а журналы меняются только записью.
func TestQuerySetsCachesParsedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.Local)
	path := filepath.Join(dir, "raw-"+now.Format(dayLayout)+".jsonl")
	line := `{"time":"` + now.Format(time.RFC3339) + `","client":"c","server":"s","tool":"t","ms":5}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.Local)
	if err := os.Chtimes(path, base, base); err != nil {
		t.Fatal(err)
	}

	var opens int32
	prev := openHistoryFile
	openHistoryFile = func(name string) (*os.File, error) {
		if name == path {
			atomic.AddInt32(&opens, 1)
		}
		return os.Open(name)
	}
	defer func() { openHistoryFile = prev }()

	src := []Source{{Set: "s", Dir: dir}}
	q := Query{Period: Period7d}
	first := QuerySets(src, q, now)
	second := QuerySets(src, q, now)
	if n := atomic.LoadInt32(&opens); n != 1 {
		t.Fatalf("файл открыт %d раз, ожидалось 1", n)
	}
	if second.Totals != first.Totals {
		t.Fatalf("итоги разошлись: %+v и %+v", first.Totals, second.Totals)
	}
	if first.Totals.Calls != 1 {
		t.Fatalf("вызовов %d, ожидалось 1", first.Totals.Calls)
	}

	// Дописанная строка меняет mtime и size — кэш обязан перечитать файл.
	if err := os.WriteFile(path, []byte(line+"\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := base.Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	third := QuerySets(src, q, now)
	if n := atomic.LoadInt32(&opens); n != 2 {
		t.Fatalf("после правки файл открыт %d раз, ожидалось 2", n)
	}
	if third.Totals.Calls != 2 {
		t.Fatalf("вызовов после правки %d, ожидалось 2", third.Totals.Calls)
	}
}

// Переполнение кэша вытесняет самую старую четверть по mtime файлов, а не всю
// карту: свежие записи переживают переполнение, новая запись добавлена, потолок
// держится.
func TestCacheEvictsOldestQuarter(t *testing.T) {
	reset := func() {
		fileCache.Lock()
		fileCache.m = map[string]cacheEntry{}
		fileCache.Unlock()
	}
	reset()
	t.Cleanup(reset)

	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.Local)
	fileCache.Lock()
	for i := 0; i < cacheMax; i++ {
		fileCache.m[fmt.Sprintf("old-%d", i)] = cacheEntry{mtime: base.Add(time.Duration(i) * time.Minute)}
	}
	fileCache.Unlock()

	dir := t.TempDir()
	path := filepath.Join(dir, "raw-2026-11-11.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cached(path, func() (any, error) { return "fresh", nil }); got != "fresh" {
		t.Fatalf("cached вернул %v, ожидалось fresh", got)
	}

	fileCache.Lock()
	defer fileCache.Unlock()
	if got := len(fileCache.m); got > cacheMax {
		t.Fatalf("размер карты %d, потолок %d", got, cacheMax)
	}
	for i := 0; i < cacheMax/4; i++ {
		if _, ok := fileCache.m[fmt.Sprintf("old-%d", i)]; ok {
			t.Fatalf("самая старая запись old-%d осталась в карте", i)
		}
	}
	for _, i := range []int{cacheMax / 4, cacheMax - 1} {
		if _, ok := fileCache.m[fmt.Sprintf("old-%d", i)]; !ok {
			t.Fatalf("свежая запись old-%d вытеснена", i)
		}
	}
	if _, ok := fileCache.m[path]; !ok {
		t.Fatalf("новая запись %q не добавлена", path)
	}
}
