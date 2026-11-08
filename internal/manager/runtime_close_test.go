package manager

import (
	"context"
	"net"
	"strconv"
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
