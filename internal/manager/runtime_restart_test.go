package manager

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
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

	rt.wait("clean", &registry.Set{ID: "clean", Port: freePort(t)}, nil, p, logFile)

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

// Рестарт идёт общим путём остановки: ровно одна строка «остановлен».
func TestRestartLogsStopOnce(t *testing.T) {
	h := &captureHandler{}
	e := newEnvLog(t, AuthOptions{Off: true}, slog.New(h))
	e.addServer(t, "fake")
	id := e.addSet(t, "restart", "fake")
	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	if _, err := e.m.rt.Restart(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("рестарт: %v", err)
	}
	defer e.m.rt.Stop(set)

	if _, n := h.find("набор " + id + ": остановлен"); n != 1 {
		t.Fatalf("строк «остановлен»: %d, ожидалась 1", n)
	}
}

// Упавший сам процесс остановки не переживает: Stop не должен писать
// «остановлен» задним числом.
func TestStopAfterCrashDoesNotClaimStopped(t *testing.T) {
	h := &captureHandler{}
	e := newEnvLog(t, AuthOptions{Off: true}, slog.New(h))
	e.addServer(t, "fake")
	id := e.addSet(t, "crash", "fake")
	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	p := e.m.rt.proc(id)
	if err := syscall.Kill(-p.pid, syscall.SIGKILL); err != nil {
		t.Fatalf("убить набор: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.m.rt.mu.Lock()
		exited := p.exited
		e.m.rt.mu.Unlock()
		if exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("процесс не отмечен упавшим")
		}
		time.Sleep(5 * time.Millisecond)
	}

	e.m.rt.Stop(set)
	if _, n := h.find("набор " + id + ": остановлен"); n != 0 {
		t.Fatalf("«остановлен» после падения: %d", n)
	}
}
