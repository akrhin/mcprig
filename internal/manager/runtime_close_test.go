package manager

import (
	"context"
	"net"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCloseStopsStartingSet: панель гаснет, пока набор ещё стартует — процесс
// обязан умереть, а порт закрыться.
func TestCloseStopsStartingSet(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "race", "fake")
	set := e.m.store.Set(id)

	done := make(chan struct{})
	go func() {
		_, _ = e.m.rt.Start(context.Background(), set, e.m.store.Servers())
		close(done)
	}()

	var p *process
	deadline := time.Now().Add(2 * time.Second)
	for p == nil && time.Now().Before(deadline) {
		p = e.m.rt.proc(id)
		time.Sleep(10 * time.Millisecond)
	}
	if p == nil {
		t.Fatal("набор не дошёл до запуска процесса")
	}
	time.Sleep(100 * time.Millisecond)
	pid := p.pid
	e.m.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start не завершился после Close")
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("процесс набора %d жив после Close", pid)
	}
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(set.Port)), 300*time.Millisecond); err == nil {
		conn.Close()
		t.Fatalf("порт %d слушает после Close", set.Port)
	}
}

// TestLoopbackAddr: проба идёт на bindIP, а для «все интерфейсы» — на loopback.
func TestLoopbackAddr(t *testing.T) {
	cases := map[string]string{
		"":         "127.0.0.1",
		"0.0.0.0":  "127.0.0.1",
		"::":       "::1",
		"[::]":     "::1",
		"10.1.2.3": "10.1.2.3",
	}
	for bindIP, want := range cases {
		if got := loopbackAddr(bindIP); got != want {
			t.Errorf("loopbackAddr(%q) = %q, ждали %q", bindIP, got, want)
		}
	}
}

// TestStartRefusesBusyPort: чужой процесс на порту набора — ошибка, а не
// «набор поднялся».
func TestStartRefusesBusyPort(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "busy", "fake")
	set := e.m.store.Set(id)

	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(set.Port)))
	if err != nil {
		t.Fatalf("занять порт %d: %v", set.Port, err)
	}
	defer ln.Close()

	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err == nil {
		t.Fatal("старт на занятом порту объявлен успешным")
	} else if !strings.Contains(err.Error(), "занят") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if p := e.m.rt.proc(id); p != nil {
		t.Fatalf("на занятом порту всё же запущен процесс %d", p.pid)
	}
}
