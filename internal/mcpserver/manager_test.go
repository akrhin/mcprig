package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcp-manager/internal/config"
)

// upstream is one in-memory downstream server with two commands. No network,
// no child process.
func upstream(ctx context.Context) (*mcp.ClientSession, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: "downstream", Version: "test"}, nil)
	for _, name := range []string{"echo", "danger-zone"} {
		s.AddTool(&mcp.Tool{
			Name:        name,
			InputSchema: objectSchema(map[string]any{"text": map[string]any{"type": "string"}}),
		}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: name + ":" + string(req.Params.Arguments),
			}}}, nil
		})
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		return nil, err
	}
	return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(context.Background(), ct, nil)
}

func testManager(cfg *config.Config, dials *int32, delay time.Duration) *Manager {
	m := New(cfg, "test")
	m.dial = func(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
		atomic.AddInt32(dials, 1)
		if delay > 0 {
			time.Sleep(delay)
		}
		return upstream(ctx)
	}
	return m
}

func clientFor(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	return res.Content[0].(*mcp.TextContent).Text
}

type serversOut struct {
	Servers []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
		Healthy bool   `json:"healthy"`
		Error   string `json:"error"`
	} `json:"servers"`
	PID int `json:"pid"`
}

func listServersOut(t *testing.T, cs *mcp.ClientSession) serversOut {
	t.Helper()
	var out serversOut
	if err := json.Unmarshal([]byte(resultText(t, call(t, cs, "list_servers", nil))), &out); err != nil {
		t.Fatalf("list_servers output: %v", err)
	}
	return out
}

func stdio(name string) *config.Server {
	return &config.Server{Name: name, Transport: config.TransportStdio, Command: []string{"downstream"}, Enabled: true}
}

// Laziness: building the manager and its server connects to nothing; the first
// list_servers does, and the session is reused after that.
func TestLazyConnect(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()

	cs := clientFor(t, m.Server())
	if n := atomic.LoadInt32(&dials); n != 0 {
		t.Fatalf("New must not connect, got %d dial(s)", n)
	}
	if out := listServersOut(t, cs); !out.Servers[0].Healthy || out.Servers[0].Type != "stdio" {
		t.Fatalf("alpha must be healthy stdio, got %+v", out.Servers[0])
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("want 1 dial after list_servers, got %d", n)
	}
	listServersOut(t, cs)
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("the session must be reused, got %d dial(s)", n)
	}
}

// Disabled and config-broken servers show unhealthy and are never dialed.
func TestUnhealthyNotDialed(t *testing.T) {
	var dials int32
	broken := stdio("broken")
	broken.ConfigError = "битый конфиг"
	off := stdio("off")
	off.Enabled = false
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha"), off, broken}}, &dials, 0)
	defer m.Close()

	out := listServersOut(t, clientFor(t, m.Server()))
	byName := map[string]struct {
		Enabled bool
		Healthy bool
		Error   string
	}{}
	for _, s := range out.Servers {
		byName[s.Name] = struct {
			Enabled bool
			Healthy bool
			Error   string
		}{s.Enabled, s.Healthy, s.Error}
	}
	if !byName["alpha"].Healthy {
		t.Error("alpha must be healthy")
	}
	if byName["off"].Healthy || byName["off"].Enabled {
		t.Error("off must be disabled and unhealthy")
	}
	if byName["broken"].Healthy || byName["broken"].Error != "битый конфиг" {
		t.Errorf("broken must be unhealthy with its error, got %+v", byName["broken"])
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("only the healthy server may be dialed, got %d dial(s)", n)
	}
}

// Single-flight: two concurrent calls to one server share one connect.
func TestSingleFlight(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 50*time.Millisecond)
	defer m.Close()
	cs := clientFor(t, m.Server())

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "invoke_command",
				Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{"text": "hi"}},
			})
			if err != nil {
				errs <- err
				return
			}
			if res.IsError {
				errs <- fmt.Errorf("isError: %s", res.Content[0].(*mcp.TextContent).Text)
				return
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("invoke_command: %v", err)
		}
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("want 1 connect for two concurrent calls, got %d", n)
	}
}

// A deny rule blocks the call before any connect, with the lazy-mcp policy shape.
func TestPermissionDeny(t *testing.T) {
	var dials int32
	srv := stdio("alpha")
	srv.Permissions = &config.Permissions{Rules: []config.Rule{
		{Pattern: "danger-*", Action: config.PermissionDeny, Message: "только чтение"},
	}}
	m := testManager(&config.Config{Servers: []*config.Server{srv}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	blocked := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "danger-zone", "parameters": map[string]any{},
	})
	if !blocked.IsError {
		t.Fatal("a denied command must return isError")
	}
	if got := blocked.Meta["lazyMcpPolicy"]; got != "blocked" {
		t.Fatalf("_meta.lazyMcpPolicy: want blocked, got %v", got)
	}
	if text := resultText(t, blocked); !strings.Contains(text, "только чтение") {
		t.Errorf("the rule message must be surfaced, got %q", text)
	}
	if n := atomic.LoadInt32(&dials); n != 0 {
		t.Fatalf("a blocked call must not connect, got %d dial(s)", n)
	}

	allowed := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "parameters": map[string]any{"text": "hi"},
	})
	if allowed.IsError {
		t.Fatalf("an unlisted command must be allowed: %s", resultText(t, allowed))
	}
	if text := resultText(t, allowed); !strings.Contains(text, `"text":"hi"`) {
		t.Errorf("downstream result must pass through, got %q", text)
	}
}
