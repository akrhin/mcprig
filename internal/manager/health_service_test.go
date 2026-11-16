package manager

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/httplisten"
	"github.com/akrhin/mcprig/internal/mcpserver"
	"github.com/akrhin/mcprig/internal/stats"
)

// Проба здоровья не должна выглядеть в наборе клиентским вызовом: у набора,
// к которому не обращался ни один клиент, статистика остаётся строго нулевой,
// сколько бы раз панель его ни опрашивала.
func TestFetchSetHealthLeavesStatsEmpty(t *testing.T) {
	rec := stats.NewRecorder("probe", 8)
	set := mcpserver.New(&config.Config{}, "test", nil)
	set.Stats = rec
	h, err := httplisten.New(
		&config.Config{Transport: &config.HTTPTransport{
			AuthToken:    "probe-token",
			AllowedHosts: []string{"127.0.0.1"},
		}},
		set.Server(),
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		rec,
	)
	if err != nil {
		t.Fatalf("httplisten.New: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	host, portText, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	// Имя, которого в наборе нет, получает отметку только после настоящего
	// обмена list_servers: без него «ноль в статистике» ничего не доказывает.
	got := fetchSetHealth(context.Background(), host, port, "probe-token", false, []string{"ghost"}, "test")
	if !got.Running || len(got.Servers) != 1 {
		t.Fatalf("проба не дошла до набора: %+v", got)
	}
	if got.Servers[0].Error == "" {
		t.Fatalf("list_servers ответил, но имя ghost не помечено: %+v", got.Servers[0])
	}

	snap := rec.Snapshot()
	if len(snap.Counters) != 0 || len(snap.Tail) != 0 {
		t.Fatalf("проба попала в статистику набора: counters=%+v tail=%+v", snap.Counters, snap.Tail)
	}
}
