package sandbox_test

import (
	"os"
	"testing"

	"github.com/akrhin/mcprig/internal/downstream"
)

// TestMain перехватывает скрытый режим child-guard: набор спавнит сторожем
// os.Executable(), а в тестах это тестовый бинарник. Без перехвата тестовый
// бинарник молча запускает тесты вместо сервера, и песочница «протекает».
func TestMain(m *testing.M) {
	downstream.RunChildGuardMode(os.Args)
	os.Exit(m.Run())
}
