package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
)

// upstream is one in-memory downstream server with two commands. No network,
// no child process.
func upstream(ctx context.Context) (*mcp.ClientSession, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: "downstream", Version: "test"}, nil)
	for _, name := range []string{"echo", "danger-zone", "boom"} {
		s.AddTool(&mcp.Tool{
			Name:        name,
			InputSchema: objectSchema(map[string]any{"text": map[string]any{"type": "string"}}),
		}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if name == "boom" {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
					Text: "апстрим упал",
				}}}, nil
			}
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
		Unknown bool     `json:"unknown"`
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

// The unknown flag must reach the client: the source value is tested above,
// the delivery is what a regression silently breaks.
func TestListServersReportsUnknown(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	t.Cleanup(func() {
		for {
			select {
			case <-probeSlots:
			default:
				return
			}
		}
	})
	for i := 0; i < maxProbes; i++ {
		probeSlots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res, err := m.listServers(ctx, nil)
	if err != nil {
		t.Fatalf("list_servers: %v", err)
	}
	var out struct {
		Servers []struct {
			Healthy bool `json:"healthy"`
			Unknown bool `json:"unknown"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("list_servers output: %v", err)
	}
	if len(out.Servers) != 1 || !out.Servers[0].Healthy || !out.Servers[0].Unknown {
		t.Fatalf("проба без слота: ждём healthy:true, unknown:true, got %+v", out.Servers)
	}
}

// A probe that never got a slot is not a sick server: the outcome is unknown,
// so it must not land in the cache as unhealthy for the whole TTL.
func TestStatusSkippedProbeNotCached(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	e := m.entries[0]
	drain := func() {
		for {
			select {
			case <-probeSlots:
			default:
				return
			}
		}
	}
	t.Cleanup(drain)

	for i := 0; i < maxProbes; i++ {
		probeSlots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if ok, errText, unknown := m.status(ctx, e); !ok || errText != "" || !unknown {
		t.Fatalf("проба без слота: надо healthy=true, unknown=true, got %v %q unknown=%v", ok, errText, unknown)
	}
	e.healthMu.Lock()
	h := e.health
	e.healthMu.Unlock()
	if !h.at.IsZero() {
		t.Fatalf("пропущенная проба не должна попадать в кэш, got %+v", h)
	}
	if n := atomic.LoadInt32(&dials); n != 0 {
		t.Fatalf("проба без слота не должна подключаться, got %d dial(s)", n)
	}

	drain()
	if ok, errText, unknown := m.status(context.Background(), e); !ok || errText != "" || unknown {
		t.Fatalf("слот свободен: проба должна пройти, got %v %q unknown=%v", ok, errText, unknown)
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("после освобождения слота проба обязана пройти, got %d dial(s)", n)
	}
	e.healthMu.Lock()
	h = e.health
	e.healthMu.Unlock()
	if h.at.IsZero() || !h.ok {
		t.Fatalf("настоящий исход должен попасть в кэш, got %+v", h)
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
	if len(one) != 3 {
		t.Fatalf("want echo, danger-zone и boom, got %+v", one)
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
	if len(many) != 2 || len(many["alpha"]) != 3 || len(many["beta"]) != 3 {
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

// list_commands names the field command; invoke_command must accept that name
// as a synonym for command_name.
func TestInvokeCommandCommandAlias(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command": "echo", "parameters": map[string]any{"text": "hi"},
	})
	if res.IsError {
		t.Fatalf("command must be accepted as an alias for command_name: %s", resultText(t, res))
	}
	if text := resultText(t, res); !strings.Contains(text, `echo:{"text":"hi"}`) {
		t.Fatalf("want the echo answer, got %q", text)
	}
	if n := atomic.LoadInt32(&dials); n == 0 {
		t.Fatal("an accepted call must dial the server")
	}
}

// Два имени одного поля с разными значениями — отказ, а не тихий выбор.
func TestInvokeCommandAliasConflict(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "command": "other",
	})
	if !res.IsError {
		t.Fatal("расходящиеся имена должны отвергаться")
	}
	if text := resultText(t, res); !strings.Contains(text, "укажите одно: command_name или command") {
		t.Fatalf("нет подсказки выбрать одно имя: %q", text)
	}
	if n := atomic.LoadInt32(&dials); n != 0 {
		t.Fatalf("отвергнутый вызов не должен ходить в апстрим: %d", n)
	}
}

// A wrong JSON type names the key and the expected kind, not a Go struct field.
func TestInvokeCommandTypeError(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{"server": 5})
	if !res.IsError {
		t.Fatal("a number for server must be an error")
	}
	if text := resultText(t, res); !strings.Contains(text, "ключ server: ожидался тип «строка», получен тип «число»") {
		t.Fatalf("want a readable type error, got %q", text)
	}
	if n := atomic.LoadInt32(&dials); n != 0 {
		t.Fatalf("a refused call must not dial, got %d", n)
	}
}

// Без обоих имён вызов отвергается до подключения к апстриму.
func TestInvokeCommandNoName(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{"server": "alpha"})
	if !res.IsError {
		t.Fatal("без command_name вызов должен отвергаться")
	}
	if text := resultText(t, res); !strings.Contains(text, "укажите command_name или command") {
		t.Fatalf("нет требования имени: %q", text)
	}
	if n := atomic.LoadInt32(&dials); n != 0 {
		t.Fatalf("отвергнутый вызов не должен ходить в апстрим: %d", n)
	}
}

// Аргументы — строка, а не объект: ошибка называет вид значения, не Go-тип map.
func TestInvokeCommandArgumentsNotObject(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "invoke_command", Arguments: "abc",
	})
	if err != nil {
		t.Fatalf("вызов: %v", err)
	}
	if !res.IsError {
		t.Fatal("строка вместо аргументов должна быть ошибкой")
	}
	if text := resultText(t, res); !strings.Contains(text, "аргументы: ожидался тип «объект», получен тип «строка»") {
		t.Fatalf("хочется понятную ошибку типа, получено %q", text)
	}
	if n := atomic.LoadInt32(&dials); n != 0 {
		t.Fatalf("отвергнутый вызов не должен ходить в апстрим: %d", n)
	}
}

// Схема не требует оба имени: клиент, проверяющий required, примет любое.
func TestInvokeCommandSchemaAcceptsEitherName(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var schema map[string]any
	for _, tool := range res.Tools {
		if tool.Name == "invoke_command" {
			schema = tool.InputSchema.(map[string]any)
		}
	}
	if schema == nil {
		t.Fatal("нет схемы invoke_command")
	}
	if required, ok := schema["required"].([]any); ok && len(required) > 0 {
		if required[0] != "server" {
			t.Fatalf("обязательным должен быть только server, получено %v", required)
		}
	}
	alts, ok := schema["anyOf"].([]any)
	if !ok || len(alts) != 2 {
		t.Fatalf("нужен anyOf из двух имён, получено %v", schema["anyOf"])
	}
}

// The stray-key error lists what is allowed, so the alias is discoverable.
func TestInvokeCommandExtraKeyListsAllowed(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "bogus": 1,
	})
	if !res.IsError {
		t.Fatal("an unknown key must be an error")
	}
	text := resultText(t, res)
	if !strings.Contains(text, "допустимые:") ||
		!strings.Contains(text, "command, parameters") {
		t.Fatalf("the error must list the allowed keys, got %q", text)
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

// Ошибка инструмента, пришедшая содержимым (isError), — это провал в счётчике.
func TestStatsCountToolErrorAsFailure(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	m.Stats = stats.NewRecorder("t", 8)
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "boom", "parameters": map[string]any{},
	})
	if !res.IsError {
		t.Fatalf("ошибка инструмента не дошла клиенту: %s", resultText(t, res))
	}
	snap := m.Stats.Snapshot()
	if len(snap.Counters) != 1 || snap.Counters[0].Failures != 1 || snap.Counters[0].Calls != 1 {
		t.Fatalf("счётчик: %+v", snap.Counters)
	}
	if len(snap.Tail) != 1 || !snap.Tail[0].Failed || !strings.Contains(snap.Tail[0].Message, "апстрим упал") {
		t.Fatalf("хвост: %+v", snap.Tail)
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

// Деградация песочницы должна быть видна в list_servers, а не только в логе.
func TestIsolationNote(t *testing.T) {
	boxed := &entry{srv: &config.Server{Sandbox: &config.Sandbox{Mode: config.SandboxModeBwrap}}}
	if got := boxed.isolationNote(); got != "bwrap" {
		t.Fatalf("изоляция: %q", got)
	}
	boxed.sandboxMu.Lock()
	boxed.sandboxNote = "bwrap не найден в PATH"
	boxed.sandboxMu.Unlock()
	if got := boxed.isolationNote(); !strings.HasPrefix(got, "без изоляции: ") {
		t.Fatalf("деградация не помечена: %q", got)
	}
	if got := (&entry{srv: &config.Server{}}).isolationNote(); got != "" {
		t.Fatalf("сервер без песочницы помечен: %q", got)
	}
}

// Отвергнутый конфиг не стартует: isolation не должен обещать ему bwrap.
func TestIsolationNoteRejectedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	body := `{"servers": [
		{"name": "naked", "command": ["node", "srv.js"], "tags": ["untrusted"]},
		{"name": "warned", "command": ["node", "srv.js"], "tags": ["untrusted"],
		 "sandbox": {"mode": "bwrap", "onUnavailable": "warn"}}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, srv := range cfg.Servers {
		if srv.ConfigError == "" {
			t.Fatalf("%s: конфиг не отвергнут", srv.Name)
		}
		if got := (&entry{srv: srv}).isolationNote(); got != "нет: конфиг отвергнут" {
			t.Fatalf("%s: isolation %q обещает изоляцию отвергнутому серверу", srv.Name, got)
		}
	}
}
