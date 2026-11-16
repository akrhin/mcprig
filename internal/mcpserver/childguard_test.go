package mcpserver

import (
	"os"
	"testing"

	"github.com/akrhin/mcprig/internal/downstream"
)

// TestMain: набор спавнит stdio-серверы сторожем (child-guard), а в тестах
// os.Executable() — этот бинарник.
func TestMain(m *testing.M) {
	downstream.RunChildGuardMode(os.Args)
	os.Exit(m.Run())
}
