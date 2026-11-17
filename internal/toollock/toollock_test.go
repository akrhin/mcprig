package toollock

// Файл пишут два процесса — прокси набора и панель, — поэтому запись идёт
// через Update под flock: без него поздняя правка затирала чужое одобрение.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tools.lock.json")
}

// Два одобрения разных инструментов одновременно: обе записи на месте.
func TestUpdateSerialisesParallelApprovals(t *testing.T) {
	path := testPath(t)
	if err := Write(path, File{Tools: map[string]Entry{
		"a/x": {Hash: "old-a", Seen: "new-a", ApprovedAt: "2026-11-15T00:00:00Z", ApprovedBy: "auto"},
		"b/y": {Hash: "old-b", Seen: "new-b", ApprovedAt: "2026-11-15T00:00:00Z", ApprovedBy: "auto"},
	}}); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	var wg sync.WaitGroup
	for _, key := range []string{"a/x", "b/y"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			err := Update(path, func(f *File) bool {
				approved, ok := Approve(f.Tools[key], "panel", "2026-11-15T00:00:01Z")
				if !ok {
					return false
				}
				f.Tools[key] = approved
				return true
			})
			if err != nil {
				t.Errorf("одобрение %s: %v", key, err)
			}
		}(key)
	}
	wg.Wait()

	got := Read(path).Tools
	for key, want := range map[string]string{"a/x": "new-a", "b/y": "new-b"} {
		if got[key].Hash != want || got[key].Seen != "" {
			t.Fatalf("%s: одобрение потеряно: %+v", key, got[key])
		}
	}
}

// Update без изменений файл не трогает: лишняя запись — лишний шанс затереть
// чужую правку.
func TestUpdateWithoutChangeWritesNothing(t *testing.T) {
	path := testPath(t)
	if err := Write(path, File{Tools: map[string]Entry{
		"a/x": {Hash: "h", ApprovedAt: "2026-11-15T00:00:00Z", ApprovedBy: "auto"},
	}}); err != nil {
		t.Fatalf("подготовка: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(*File) bool { return false }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("файл перезаписан без изменений")
	}
}

// Битый файл — ошибка, а не пустые одобрения: иначе Update одобрил бы всё
// заново как новое, и карантин снялся бы сам.
func TestUpdateRefusesCorruptFile(t *testing.T) {
	path := testPath(t)
	const garbage = "{не json"
	if err := os.WriteFile(path, []byte(garbage), 0o600); err != nil {
		t.Fatalf("подготовка: %v", err)
	}
	called := false
	err := Update(path, func(*File) bool {
		called = true
		return true
	})
	if err == nil {
		t.Fatal("битый файл принят за пустой")
	}
	if called {
		t.Fatal("правка вызвана на неразобранном файле")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("файл: %v", err)
	}
	if string(raw) != garbage {
		t.Fatalf("битый файл переписан: %q", raw)
	}
}

// Отсутствующий файл — обычный старт набора: правка применяется и создаёт его.
func TestUpdateCreatesMissingFile(t *testing.T) {
	path := testPath(t)
	err := Update(path, func(f *File) bool {
		f.Tools["a/x"] = Entry{Hash: "h", ApprovedAt: "2026-11-15T00:00:00Z", ApprovedBy: "auto"}
		return true
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := Read(path).Tools["a/x"]; !ok {
		t.Fatal("запись не создана")
	}
}
