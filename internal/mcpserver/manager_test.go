package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/stats"
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
	m := New(cfg, "test", nil)
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
		Name    string   `json:"name"`
		Type    string   `json:"type"`
		Enabled bool     `json:"enabled"`
		Healthy bool     `json:"healthy"`
		Error   string   `json:"error"`
		Tags    []string `json:"tags"`
	} `json:"servers"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
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

// A disabled server is switched off, not broken: enabled:false, healthy:true,
// and it is never dialed. A config-broken one is unhealthy with its reason.
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
	if !byName["off"].Healthy || byName["off"].Enabled {
		t.Error("off must be disabled and healthy: switched off, not broken")
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

// ─── contract tests ──────────────────────────────────────────────────────────

// The filter sees tags and is applied before probing: a server filtered out is
// never dialed.
func TestListServersFilterByTag(t *testing.T) {
	var dials int32
	db := stdio("alpha")
	db.Tags = []string{"database", "postgres"}
	search := stdio("beta")
	search.Tags = []string{"web-search"}
	m := testManager(&config.Config{Servers: []*config.Server{db, search}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "list_servers", map[string]any{"filter": "postgres"})
	var out serversOut
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("list_servers output: %v", err)
	}
	if len(out.Servers) != 1 || out.Servers[0].Name != "alpha" {
		t.Fatalf("filter by tag must keep alpha only, got %+v", out.Servers)
	}
	if out.Version != "test" {
		t.Errorf("version: want test, got %q", out.Version)
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("the filtered-out server must not be dialed, got %d dial(s)", n)
	}
}

// A failed health probe is cached for the TTL too, not retried on every list.
func TestHealthCacheKeepsFailures(t *testing.T) {
	var dials int32
	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	m.dial = func(context.Context, *config.Server) (*mcp.ClientSession, error) {
		atomic.AddInt32(&dials, 1)
		return nil, errors.New("нет связи")
	}
	defer m.Close()
	cs := clientFor(t, m.Server())

	for i := 0; i < 2; i++ {
		out := listServersOut(t, cs)
		if out.Servers[0].Healthy || out.Servers[0].Error != "нет связи" {
			t.Fatalf("want unhealthy with the dial error, got %+v", out.Servers[0])
		}
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("the cached failure must not re-dial, got %d dial(s)", n)
	}
}

// list_commands answers an array for `server`, an object for `servers`, and
// errors when neither is given.
func TestListCommandsForms(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha"), stdio("beta")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	single := call(t, cs, "list_commands", map[string]any{"server": "alpha"})
	var one []struct {
		Name    string `json:"name"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(resultText(t, single)), &one); err != nil {
		t.Fatalf("single-server answer must be an array: %v", err)
	}
	if len(one) != 2 {
		t.Fatalf("want echo and danger-zone, got %+v", one)
	}
	for _, cmd := range one {
		if cmd.Name != cmd.Command {
			t.Fatalf("name and command must agree, got %+v", cmd)
		}
	}
	if one[0].Name != "danger-zone" && one[1].Name != "danger-zone" {
		t.Fatalf("want danger-zone among the commands, got %+v", one)
	}

	multi := call(t, cs, "list_commands", map[string]any{"servers": []string{"alpha", "beta"}})
	var many map[string][]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(resultText(t, multi)), &many); err != nil {
		t.Fatalf("servers answer must be an object: %v", err)
	}
	if len(many) != 2 || len(many["alpha"]) != 2 || len(many["beta"]) != 2 {
		t.Fatalf("want both servers keyed, got %+v", many)
	}

	none := call(t, cs, "list_commands", nil)
	if !none.IsError || !strings.Contains(resultText(t, none), "нужен параметр server или servers") {
		t.Fatalf("missing server and servers must be an error, got %q", resultText(t, none))
	}
}

// Top-level keys that belong inside parameters are refused instead of silently
// dropped.
func TestInvokeCommandStrictArguments(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	bad := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "path": "/x", "query": "y",
	})
	if !bad.IsError {
		t.Fatal("an unexpected top-level key must be an error")
	}
	if text := resultText(t, bad); !strings.Contains(text, "лишние ключи: path, query") ||
		!strings.Contains(text, "parameters") {
		t.Fatalf("the error must name the stray keys and point at parameters, got %q", text)
	}
	if n := atomic.LoadInt32(&dials); n != 0 {
		t.Fatalf("a refused call must not dial, got %d", n)
	}

	// parameters: null is an empty object, not an error.
	null := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "parameters": nil,
	})
	if null.IsError {
		t.Fatalf("parameters:null must be accepted: %s", resultText(t, null))
	}
	if text := resultText(t, null); !strings.Contains(text, "echo:{}") {
		t.Errorf("parameters:null must pass an empty object, got %q", text)
	}
}

// One absent command does not hide the schemas that do exist.
func TestDescribeCommandsSkipsMissing(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "describe_commands", map[string]any{
		"server": "alpha", "command_names": []string{"echo", "nope"},
	})
	if res.IsError {
		t.Fatalf("a missing name must not fail the whole call: %s", resultText(t, res))
	}
	var out struct {
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
		Missing []string `json:"missing"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("describe_commands output: %v", err)
	}
	if len(out.Commands) != 1 || out.Commands[0].Name != "echo" {
		t.Fatalf("want the echo schema, got %+v", out.Commands)
	}
	if len(out.Missing) != 1 || out.Missing[0] != "nope" {
		t.Fatalf("want missing [nope], got %v", out.Missing)
	}
}

func pagedUpstream(ctx context.Context) (*mcp.ClientSession, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: "paged", Version: "test"}, &mcp.ServerOptions{PageSize: 1})
	for _, name := range []string{"one", "two", "three"} {
		s.AddTool(&mcp.Tool{Name: name, InputSchema: objectSchema(map[string]any{})},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name}}}, nil
			})
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		return nil, err
	}
	return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(context.Background(), ct, nil)
}

// Every page of tools/list is read, not only the first.
func TestListCommandsPaginates(t *testing.T) {
	var dials int32
	m := New(&config.Config{Servers: []*config.Server{stdio("paged")}}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		atomic.AddInt32(&dials, 1)
		return pagedUpstream(ctx)
	}
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "list_commands", map[string]any{"server": "paged"})
	var cmds []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &cmds); err != nil {
		t.Fatalf("list_commands output: %v", err)
	}
	if len(cmds) != 3 {
		t.Fatalf("want all 3 pages of commands, got %+v", cmds)
	}
}

// A cached session whose process died is replaced with exactly one reconnect.
func TestReconnectAfterClosedSession(t *testing.T) {
	var dials int32
	dead, err := upstream(context.Background())
	if err != nil {
		t.Fatalf("dead session: %v", err)
	}
	_ = dead.Close()

	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		if atomic.AddInt32(&dials, 1) == 1 {
			return dead, nil
		}
		return upstream(ctx)
	}
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "parameters": map[string]any{"text": "hi"},
	})
	if res.IsError {
		t.Fatalf("the call must succeed after one reconnect: %s", resultText(t, res))
	}
	if n := atomic.LoadInt32(&dials); n != 2 {
		t.Fatalf("want exactly one reconnect, got %d dial(s)", n)
	}
}

// Cancelling one waiting call must not abort the connect another call shares.
func TestWaiterCancelKeepsSharedConnect(t *testing.T) {
	var dials int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		atomic.AddInt32(&dials, 1)
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return upstream(ctx)
	}
	defer m.Close()
	cs := clientFor(t, m.Server())

	ctx1, cancel1 := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(ctx1, &mcp.CallToolParams{
			Name:      "invoke_command",
			Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{"text": "one"}},
		})
		firstDone <- err
	}()
	<-started
	cancel1()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancelled waiter must return context.Canceled, got %v", err)
	}

	secondDone := make(chan error, 1)
	go func() {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "invoke_command",
			Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{"text": "two"}},
		})
		if err == nil && res.IsError {
			err = errors.New(resultText(t, res))
		}
		secondDone <- err
	}()
	close(release)
	if err := <-secondDone; err != nil {
		t.Fatalf("the second waiter must get the shared session: %v", err)
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("want one shared connect, got %d dial(s)", n)
	}
}

// Close during a hung connect returns within its 5 s bound and tells the
// waiting call that the manager is closed.
func TestCloseDuringHungConnect(t *testing.T) {
	started := make(chan struct{}, 1)
	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cs := clientFor(t, m.Server())

	callDone := make(chan *mcp.CallToolResult, 1)
	go func() {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "invoke_command",
			Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{}},
		})
		if err != nil {
			callDone <- &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
			return
		}
		callDone <- res
	}()
	<-started

	start := time.Now()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("Close must not hang, took %s", elapsed)
	}
	select {
	case res := <-callDone:
		if !res.IsError || !strings.Contains(resultText(t, res), "менеджер закрыт") {
			t.Fatalf("a waiting call must be told the manager is closed, got %q", resultText(t, res))
		}
	case <-time.After(6 * time.Second):
		t.Fatal("the waiting call never returned")
	}
}

func TestStatsCountCallsPerClient(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	m.Stats = stats.NewRecorder("t", 8)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "parameters": map[string]any{"text": "hi"},
	})
	if res.IsError {
		t.Fatalf("invoke: %s", resultText(t, res))
	}

	snap := m.Stats.Snapshot()
	if len(snap.Counters) != 1 {
		t.Fatalf("counters = %+v", snap.Counters)
	}
	c := snap.Counters[0]
	if c.Client != "test-client/test" || c.Tool != "echo" || c.Calls != 1 || c.Failures != 0 {
		t.Fatalf("counter = %+v", c)
	}
	if len(snap.Tail) != 1 || snap.Tail[0].Server != "alpha" {
		t.Fatalf("tail = %+v", snap.Tail)
	}
}

// Неизвестное имя инструмента не заводит счётчик: иначе любое придуманное
// command_name оседает в памяти.
func TestStatsSkipUnknownTool(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	m.Stats = stats.NewRecorder("t", 8)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "no-such-tool", "parameters": map[string]any{},
	})
	if !res.IsError {
		t.Fatalf("неизвестный инструмент прошёл успешно: %s", resultText(t, res))
	}
	if snap := m.Stats.Snapshot(); len(snap.Counters) != 0 || len(snap.Tail) != 0 {
		t.Fatalf("счётчики/хвост от неизвестного имени: %+v", snap)
	}
}
