package manager

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/sintez/mcprig/internal/registry"
)

// TestCleanExitDoesNotRestart: код 0 — штатный выход, бюджет рестартов не
// тратится.
func TestCleanExitDoesNotRestart(t *testing.T) {
	rt := NewRuntime(t.TempDir(), "/bin/true", "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "log-*")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	p := &process{cmd: cmd, pid: cmd.Process.Pid, done: make(chan struct{})}
	rt.mu.Lock()
	rt.procs["clean"] = p
	rt.mu.Unlock()

	rt.wait("clean", &registry.Set{ID: "clean", Port: 9100}, nil, p, logFile)

	rt.mu.Lock()
	restarts := p.restarts
	rt.mu.Unlock()
	if restarts != 0 {
		t.Fatalf("выход с кодом 0 дал перезапуск: restarts=%d", restarts)
	}
}

// TestStartResetsRestartBudget: ручной start не наследует исчерпанный бюджет.
func TestStartResetsRestartBudget(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "budget", "fake")
	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	defer e.m.rt.Stop(set)

	e.m.rt.mu.Lock()
	e.m.rt.procs[id].restarts = maxRestarts
	e.m.rt.mu.Unlock()

	status, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers())
	if err != nil {
		t.Fatalf("повторный старт: %v", err)
	}
	if status.Restarts != 0 {
		t.Fatalf("ручной start наследует бюджет: restarts=%d", status.Restarts)
	}
}
