package main

import (
	"os"
	"testing"

	"github.com/akrhin/mcprig/internal/downstream"
)

// TestMain: набор спавнит stdio-серверы сторожем (child-guard), а в тестах
// os.Executable() — этот бинарник. Без перехвата проба и warm поднимали бы
// рекурсивно весь набор тестов вместо сервера.
func TestMain(m *testing.M) {
	downstream.RunChildGuardMode(os.Args)
	os.Exit(m.Run())
}
