package mcpserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// pingDial answers one in-memory server with a ping tool, so a test can prove
// the next connect works after a failed one.
func pingDial(t *testing.T) dialer {
	t.Helper()
	return func(context.Context, *config.Server) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "downstream", Version: "test"}, nil)
		s.AddTool(&mcp.Tool{
			Name:        "ping",
			Description: "ping",
			InputSchema: objectSchema(map[string]any{}),
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		})
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "mcprig", Version: "test"}, nil).
			Connect(context.Background(), ct, nil)
	}
}

// A hung initialize must surface as an error instead of holding c.try forever,
// and the next call must dial again instead of inheriting the failure.
func TestConnectTimeoutReportsAndRetries(t *testing.T) {
	orig := connectTimeout
	connectTimeout = 50 * time.Millisecond
	t.Cleanup(func() { connectTimeout = orig })

	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	t.Cleanup(func() { _ = m.Close() })

	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	cs := clientFor(t, m.Server())
	text := resultText(t, call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "ping",
	}))
	if !strings.Contains(text, "подключение не уложилось в") {
		t.Fatalf("нет ошибки таймаута коннекта: %q", text)
	}

	// The dead attempt must not stick: a working dial right after is used.
	m.dial = pingDial(t)
	text = resultText(t, call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "ping",
	}))
	if !strings.Contains(text, "pong") {
		t.Fatalf("повторный вызов не сконнектился заново: %q", text)
	}
}

// A server with an oauth block waits for a human at the browser: the plain
// 30 s deadline would kill the first authorization, so the connect gets minutes.
func TestConnectDeadlineForOAuth(t *testing.T) {
	oauthSrv := stdio("oauth")
	oauthSrv.OAuth = &config.OAuth{}
	m := New(&config.Config{Servers: []*config.Server{stdio("plain"), oauthSrv}}, "test", nil)
	t.Cleanup(func() { _ = m.Close() })

	var mu sync.Mutex
	seen := map[string]time.Duration{}
	m.dial = func(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
		deadline, ok := ctx.Deadline()
		mu.Lock()
		if ok {
			seen[srv.Name] = time.Until(deadline)
		} else {
			seen[srv.Name] = -1
		}
		mu.Unlock()
		return nil, errors.New("соединения нет: проверяем только дедлайн")
	}

	cs := clientFor(t, m.Server())
	for _, name := range []string{"plain", "oauth"} {
		call(t, cs, "invoke_command", map[string]any{"server": name, "command_name": "ping"})
	}

	mu.Lock()
	plain, oauth := seen["plain"], seen["oauth"]
	mu.Unlock()
	if plain < 25*time.Second || plain > 35*time.Second {
		t.Fatalf("обычный сервер получил дедлайн %s, ожидалось ~30s", plain)
	}
	if oauth < 5*time.Minute+30*time.Second || oauth > 6*time.Minute+30*time.Second {
		t.Fatalf("сервер с oauth получил дедлайн %s, ожидалось ~6m", oauth)
	}
}

// Close must not claim success while a connect is still hanging: the caller
// learns the child may still be alive.
func TestCloseReportsUnfinishedConnect(t *testing.T) {
	origClose, origConnect := closeWait, connectTimeout
	closeWait = 50 * time.Millisecond
	connectTimeout = 10 * time.Second
	t.Cleanup(func() { closeWait, connectTimeout = origClose, origConnect })

	hang := make(chan struct{})
	entered := make(chan struct{})
	dialDone := make(chan struct{})
	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	m.dial = func(context.Context, *config.Server) (*mcp.ClientSession, error) {
		close(entered)
		<-hang
		close(dialDone)
		return nil, errors.New("зависший коннект")
	}

	e := m.entries[0]
	go func() { _, _ = e.conn.session(context.Background(), m.dialFor(e.srv), connectDeadline(e.srv)) }()
	<-entered
	e.conn.mu.Lock()
	a := e.conn.try
	e.conn.mu.Unlock()

	start := time.Now()
	err := m.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close ждал %s, ожидалось около closeWait", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "закрытие: подключение не завершилось") {
		t.Fatalf("Close вернул %v, ожидалась ошибка о незавершённом коннекте", err)
	}

	close(hang)
	<-dialDone
	<-a.done
}
