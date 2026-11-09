package mcpserver

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

// A connection lost before any response byte is repeated once, on a fresh
// session and after invokeRetryDelay.
func TestInvokeRetriesLostConnection(t *testing.T) {
	dead, err := upstream(context.Background())
	if err != nil {
		t.Fatalf("dead session: %v", err)
	}
	_ = dead.Close()

	var dials int32
	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		if atomic.AddInt32(&dials, 1) == 1 {
			return dead, nil
		}
		return upstream(ctx)
	}
	defer m.Close()
	cs := clientFor(t, m.Server())

	start := time.Now()
	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "parameters": map[string]any{"text": "hi"},
	})
	if res.IsError {
		t.Fatalf("вызов должен пройти после одного повтора: %s", resultText(t, res))
	}
	if n := atomic.LoadInt32(&dials); n != 2 {
		t.Fatalf("want one retry, got %d dial(s)", n)
	}
	if d := time.Since(start); d < invokeRetryDelay {
		t.Fatalf("повтор без паузы: %s < %s", d, invokeRetryDelay)
	}
}

// An answer — even isError — is not a lost connection: it is returned as is and
// the tool is not run again.
func TestInvokeNoRetryAfterResult(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "boom",
	})
	if !res.IsError {
		t.Fatal("boom must return isError")
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("ответ инструмента не повторяется, got %d dial(s)", n)
	}
}

// The classifier keeps the byte boundary and the deadline out of the retry.
func TestRetryableCall(t *testing.T) {
	cases := []struct {
		name string
		res  *mcp.CallToolResult
		err  error
		want bool
	}{
		{"closed before response", nil, mcp.ErrConnectionClosed, true},
		{"clean eof", nil, io.EOF, true},
		{"response started", nil, io.ErrUnexpectedEOF, false},
		{"tool error result", &mcp.CallToolResult{IsError: true}, nil, false},
		{"deadline", nil, context.DeadlineExceeded, false},
		{"no error", nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := retryableCall(c.res, c.err); got != c.want {
				t.Fatalf("retryableCall = %v, want %v", got, c.want)
			}
		})
	}
}
