package mcpserver

import (
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// Комментарии обеих констант обещают те же числа, что в internal/manager:
// иначе панель и прокси расходятся в том, кто «здоров». Компилятор это не
// ловит — константы неэкспортируемые и живут в разных пакетах, поэтому читаем
// объявления из исходника соседа.
func TestHealthConstantsMatchManager(t *testing.T) {
	raw, err := os.ReadFile("../manager/health.go")
	if err != nil {
		t.Fatalf("internal/manager/health.go: %v", err)
	}
	for name, mine := range map[string]time.Duration{
		"healthTTL":     healthTTL,
		"healthTimeout": healthTimeout,
	} {
		re := regexp.MustCompile(`(?m)^\s*` + name + `\s*=\s*([0-9]+)\s*\*\s*time\.Second`)
		m := re.FindSubmatch(raw)
		if m == nil {
			t.Fatalf("%s не найден в internal/manager/health.go", name)
		}
		seconds, err := strconv.Atoi(string(m[1]))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if theirs := time.Duration(seconds) * time.Second; theirs != mine {
			t.Errorf("%s: mcpserver %s, manager %s — числа обязаны совпадать", name, mine, theirs)
		}
	}
}
