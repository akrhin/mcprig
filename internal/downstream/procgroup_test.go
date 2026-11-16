package downstream

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// TestCloseKillsProcessTreeAfterTimeout: транспорт гасит только прямого
// ребёнка, поэтому внук sh -c "… & wait" переживал пробу и оставался в системе,
// переподвешенный к init. Проверяем по pid из /proc, а не по имени: чужой sleep
// дал бы ложный результат.
func TestCloseKillsProcessTreeAfterTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, pid, tree := startTree(t, ctx)

	cancel() // бюджет подключения истёк, как у пробы, не дождавшейся initialize
	start := time.Now()
	_ = conn.Close()
	elapsed := time.Since(start)
	assertTreeGone(t, pid, tree)
	if elapsed > 2*time.Second {
		t.Errorf("закрытие заняло %s: зависший сервер не должен держать дольше своего бюджета", elapsed)
	}
}

// TestCloseKillsProcessTreeOnHang: тот же сервер, но бюджет ещё жив — он не
// читает stdin и выходит только по сигналу. Закрытие обязано уложиться в
// stopGrace менеджера (5 с), иначе перезапуск набора сносит группу набора
// раньше, чем сервер погасил своих внуков.
func TestCloseKillsProcessTreeOnHang(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, pid, tree := startTree(t, ctx)

	start := time.Now()
	_ = conn.Close()
	elapsed := time.Since(start)
	assertTreeGone(t, pid, tree)
	if elapsed > 5*time.Second {
		t.Errorf("закрытие заняло %s: дольше stopGrace менеджера", elapsed)
	}
}

// startTree поднимает сервер sh -c "sleep & wait" и отдаёт его дерево: сам pid
// и всех потомков.
func startTree(t *testing.T, ctx context.Context) (conn mcp.Connection, pid int, tree []int) {
	t.Helper()
	c := &Connector{Version: "test"}
	tr, _, err := c.transport(ctx, &config.Server{
		Name:      "tree",
		Transport: config.TransportStdio,
		Command:   []string{"/bin/sh", "-c", "sleep 31111 & wait"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cn, err := tr.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid = tr.(*groupTransport).Command.Process.Pid
	return cn, pid, waitTree(t, pid, 2) // оболочка и её внук
}

// assertTreeGone: после закрытия ни одного живого процесса дерева, а внуки
// обязаны быть в группе сервера — без своей группы их не достать.
func assertTreeGone(t *testing.T, pid int, tree []int) {
	t.Helper()
	for _, p := range tree[1:] {
		if _, _, pgid, ok := procInfo(p); ok && pgid != pid {
			t.Errorf("процесс %d не в группе %d: сервер обязан жить в своей группе", p, pid)
		}
	}
	for _, p := range tree {
		if !gone(p, 2*time.Second) {
			t.Errorf("процесс %d жив после Close", p)
		}
	}
}

// waitTree ждёт, пока под pid появится не меньше n процессов, и отдаёт дерево:
// сам pid и всех его потомков.
func waitTree(t *testing.T, pid, n int) []int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tree := descendants(pid)
		if len(tree) >= n {
			return tree
		}
		if time.Now().After(deadline) {
			t.Fatalf("под %d нашлось %d процессов, ждали %d", pid, len(tree), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// descendants обходит /proc по PPid: /proc/<pid>/task/<tid>/children есть не в
// каждом ядре (нужен CONFIG_PROC_CHILDREN).
func descendants(pid int) []int {
	kids := map[int][]int{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return []int{pid}
	}
	for _, e := range entries {
		p, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if _, ppid, _, ok := procInfo(p); ok {
			kids[ppid] = append(kids[ppid], p)
		}
	}
	out := []int{pid}
	for i := 0; i < len(out); i++ {
		out = append(out, kids[out[i]]...)
	}
	return out
}

// gone ждёт смерти процесса не дольше wait. Зомби мёртв: пожинает его чужой
// PID 1, а в контейнере он этого не делает — kill(0) такого процесса отвечает
// «жив» вечно.
func gone(pid int, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if state, _, _, ok := procInfo(pid); !ok || state == "Z" {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
