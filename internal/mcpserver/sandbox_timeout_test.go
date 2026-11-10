package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/downstream"
	"github.com/akrhin/mcprig/internal/sandbox"
)

type slowIn struct {
	Text string `json:"text,omitempty"`
}

// runSlowChild — сервер-фикстура: один инструмент спит дольше timeoutSec.
func runSlowChild() {
	srv := mcp.NewServer(&mcp.Implementation{Name: "slowmcp", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "sleep", Description: "Sleeps for 3s."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ slowIn) (*mcp.CallToolResult, any, error) {
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "проснулся"}}}, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "Echo."},
		func(_ context.Context, _ *mcp.CallToolRequest, in slowIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
		})
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// timeoutSec обязан обрывать вызов, отличать свой дедлайн от родительского и
// сбрасывать сессию: иначе первый же зависший вызов залипает навсегда.
func TestSandboxCallTimeout(t *testing.T) {
	if os.Getenv("SLOW_CHILD_TEST") == "1" {
		runSlowChild()
	}
	if err := sandbox.Probe(); err != nil {
		t.Skipf("песочница недоступна: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Servers: []*config.Server{{
		Name:      "slow",
		Transport: config.TransportStdio,
		Command:   []string{exe, "-test.run=TestSandboxCallTimeout$"},
		Env:       map[string]string{"SLOW_CHILD_TEST": "1"},
		// Тестовый бинарь лежит в /tmp/go-buildNNN: без привязки его каталога
		// bwrap не найдёт exec (это же подтверждает, что изоляция применилась).
		Sandbox: &config.Sandbox{Mode: config.SandboxModeBwrap, MemoryMb: 4096, TimeoutSec: 1,
			RO: append(append([]string{}, config.DefaultSandboxRO...), filepath.Dir(exe))},
	}}}
	conn := &downstream.Connector{Version: "test", Env: downstream.EnvFor}
	m := New(cfg, "test", conn)
	defer m.Close()

	e := m.find("slow")
	if e == nil {
		t.Fatal("нет записи сервера")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	_, err = m.callTool(ctx, e, &mcp.CallToolParams{Name: "sleep"})
	if err == nil {
		t.Fatal("вызов не оборван по timeoutSec")
	}
	if !strings.Contains(err.Error(), "таймаут 1s (sandbox)") {
		t.Fatalf("ошибка не про свой таймаут: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%%w потерян: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Fatalf("вызов держался %s", elapsed)
	}

	// Сессия сброшена: следующий вызов поднимает новый процесс и отвечает.
	res, err := m.callTool(ctx, e, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "жив"}})
	if err != nil {
		t.Fatalf("после таймаута сессия не переехала: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("пустой ответ: %+v", res)
	}
}
