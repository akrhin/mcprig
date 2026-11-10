package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// fanTracker records how many downstream calls one server handled at once.
type fanTracker struct {
	mu     sync.Mutex
	active map[string]int
	max    map[string]int
}

func newFanTracker() *fanTracker {
	return &fanTracker{active: map[string]int{}, max: map[string]int{}}
}

func (f *fanTracker) enter(name string) func() {
	f.mu.Lock()
	f.active[name]++
	if f.active[name] > f.max[name] {
		f.max[name] = f.active[name]
	}
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.active[name]--
		f.mu.Unlock()
	}
}

func (f *fanTracker) peak(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.max[name]
}

// fanDownstream is a downstream with echo (optional delay), boom and big.
func fanDownstream(name string, f *fanTracker) (*mcp.ClientSession, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: name, Version: "test"}, nil)
	s.AddTool(&mcp.Tool{Name: "echo", InputSchema: objectSchema(map[string]any{
		"text": map[string]any{"type": "string"},
		"ms":   map[string]any{"type": "integer"},
	})}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in struct {
			Text string `json:"text"`
			MS   int    `json:"ms"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &in)
		done := f.enter(name)
		defer done()
		if in.MS > 0 {
			time.Sleep(time.Duration(in.MS) * time.Millisecond)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name + ":" + in.Text}}}, nil
	})
	s.AddTool(&mcp.Tool{Name: "boom", InputSchema: objectSchema(map[string]any{})},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "апстрим упал"}}}, nil
		})
	s.AddTool(&mcp.Tool{Name: "big", InputSchema: objectSchema(map[string]any{
		"n": map[string]any{"type": "integer"},
	})}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in struct {
			N int `json:"n"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &in)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", in.N)}}}, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		return nil, err
	}
	return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(context.Background(), ct, nil)
}

func fanManager(t *testing.T, f *fanTracker, cfg *config.Config, names ...string) *Manager {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	for _, name := range names {
		cfg.Servers = append(cfg.Servers, stdio(name))
	}
	m := New(cfg, "test", nil)
	m.dial = func(_ context.Context, srv *config.Server) (*mcp.ClientSession, error) {
		return fanDownstream(srv.Name, f)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func echoCall(server, text string, ms int) map[string]any {
	return map[string]any{
		"server": server, "command_name": "echo",
		"parameters": map[string]any{"text": text, "ms": ms},
	}
}

func manyAnswers(t *testing.T, res *mcp.CallToolResult) []manyAnswer {
	t.Helper()
	var out []manyAnswer
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("invoke_many output: %v", err)
	}
	return out
}

// Answers keep the order of calls even when completion order differs.
func TestInvokeManyKeepsInputOrder(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, nil, "a")
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		echoCall("a", "first", 80),
		echoCall("a", "second", 10),
		echoCall("a", "third", 40),
	}})
	if res.IsError {
		t.Fatalf("invoke_many: %s", resultText(t, res))
	}
	out := manyAnswers(t, res)
	want := []string{"a:first", "a:second", "a:third"}
	if len(out) != len(want) {
		t.Fatalf("ответов %d, want %d", len(out), len(want))
	}
	for i, text := range want {
		if !out[i].OK {
			t.Fatalf("calls[%d] не ok: %+v", i, out[i])
		}
		if got, _ := out[i].Result.(string); got != text {
			t.Fatalf("calls[%d] = %q, want %q", i, got, text)
		}
	}
	// All three ran at once: the per-server cap is 4, so the order above is
	// not an artifact of serialization.
	if peak := f.peak("a"); peak != 3 {
		t.Fatalf("параллельных вызовов %d, want 3", peak)
	}
}

// One failed call keeps its slot and does not hide the others.
func TestInvokeManyIsolatesFailure(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, nil, "a", "b", "c")
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		echoCall("a", "ok", 0),
		map[string]any{"server": "b", "command_name": "boom", "parameters": map[string]any{}},
		map[string]any{"server": "missing", "command_name": "echo", "parameters": map[string]any{}},
		echoCall("c", "ok", 0),
	}})
	if res.IsError {
		t.Fatalf("ошибка одного вызова уронила весь веер: %s", resultText(t, res))
	}
	out := manyAnswers(t, res)
	if !out[0].OK || !out[3].OK {
		t.Fatalf("успешные вызовы потерялись: %+v", out)
	}
	if out[1].OK || !strings.Contains(out[1].Error, "апстрим упал") {
		t.Fatalf("calls[1] = %+v", out[1])
	}
	if out[2].OK || !strings.Contains(out[2].Error, "неизвестный сервер") {
		t.Fatalf("calls[2] = %+v", out[2])
	}
}

func TestInvokeManyRejectsMoreThanEight(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, nil, "a")
	cs := clientFor(t, m.Server())

	calls := make([]any, 9)
	for i := range calls {
		calls[i] = echoCall("a", "x", 0)
	}
	res := call(t, cs, "invoke_many", map[string]any{"calls": calls})
	if !res.IsError {
		t.Fatal("девять вызовов должны быть отвергнуты")
	}
	if text := resultText(t, res); !strings.Contains(text, "не больше 8") {
		t.Fatalf("ошибка не называет предел: %q", text)
	}
}

// The same truncateResult/maxBytes cap applies to every fan-out answer.
func TestInvokeManyTruncatesResults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPRIG_DATA_DIR", dir)
	f := newFanTracker()
	m := fanManager(t, f, nil, "big")
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_many", map[string]any{
		"calls": []any{map[string]any{
			"server": "big", "command_name": "big", "parameters": map[string]any{"n": 50000},
		}},
		"maxBytes": 1000,
	})
	out := manyAnswers(t, res)
	if !out[0].OK {
		t.Fatalf("вызов не ok: %+v", out[0])
	}
	text, _ := out[0].Result.(string)
	if !strings.Contains(text, "[обрезано: 1000 из 50000 байт, полный ответ — ") {
		t.Fatalf("нет маркера усечения: %q", text[:120])
	}
	if !strings.HasSuffix(text, "]") {
		t.Fatalf("маркер не закрыт")
	}
}

// One downstream never sees more than four simultaneous calls in a fan-out.
func TestInvokeManyCapsCallsPerServer(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, nil, "same")
	cs := clientFor(t, m.Server())

	calls := make([]any, 6)
	for i := range calls {
		calls[i] = echoCall("same", "x", 40)
	}
	out := manyAnswers(t, call(t, cs, "invoke_many", map[string]any{"calls": calls}))
	for i, ans := range out {
		if !ans.OK {
			t.Fatalf("calls[%d] не ok: %+v", i, ans)
		}
	}
	if peak := f.peak("same"); peak != maxPerServer {
		t.Fatalf("одновременных вызовов к серверу %d, want %d", peak, maxPerServer)
	}
}

// requestTimeout bounds the whole fan-out, not each call.
func TestInvokeManyTimeoutBoundsWholeBatch(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, &config.Config{RequestTimeout: 40}, "a")
	cs := clientFor(t, m.Server())

	start := time.Now()
	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		echoCall("a", "slow", 500),
		echoCall("a", "fast", 0),
	}})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("веер держался %s при requestTimeout 40 мс", elapsed)
	}
	out := manyAnswers(t, res)
	for i, ans := range out {
		if ans.OK {
			continue
		}
		if !strings.Contains(ans.Error, "таймаут вызова 40 мс") {
			t.Fatalf("calls[%d]: ошибка не называет таймаут: %q", i, ans.Error)
		}
	}
}

// Неизвестное имя в пачке отвергается по кэшу, как в invoke_command: в апстрим
// уходит только известный вызов, тексты обоих вызовов свои.
func TestInvokeManyChecksCache(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, &dials, 0)
	defer m.Close()
	m.storeTools(m.find("alpha"), []*mcp.Tool{{Name: "echo"}})
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		echoCall("alpha", "ok", 0),
		map[string]any{"server": "alpha", "command_name": "nope", "parameters": map[string]any{}},
	}})
	if res.IsError {
		t.Fatalf("веер упал: %s", resultText(t, res))
	}
	out := manyAnswers(t, res)
	if !out[0].OK {
		t.Fatalf("известный вызов не ok: %+v", out[0])
	}
	if got, _ := out[0].Result.(string); !strings.Contains(got, "echo:") || !strings.Contains(got, `"ok"`) {
		t.Fatalf("ответ не от апстрима: %q", got)
	}
	if out[1].OK || out[1].Error != `неизвестный инструмент "nope" у сервера "alpha"` {
		t.Fatalf("calls[1] = %+v", out[1])
	}
	if got := atomic.LoadInt32(&dials); got != 1 {
		t.Fatalf("соединений %d, ожидалось 1", got)
	}
}

// Bad input fails the whole request; nothing runs.
func TestInvokeManyRejectsBadInput(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, nil, "a")
	cs := clientFor(t, m.Server())

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"empty", map[string]any{"calls": []any{}}, "хотя бы один"},
		{"extra key", map[string]any{"calls": []any{map[string]any{
			"server": "a", "command_name": "echo", "bogus": 1,
		}}}, "calls[0]: лишние ключи: bogus"},
		{"no command", map[string]any{"calls": []any{map[string]any{
			"server": "a",
		}}}, "calls[0]: укажите command_name или command"},
		{"command conflicts with command_name", map[string]any{"calls": []any{map[string]any{
			"server": "a", "command_name": "echo", "command": "boom",
		}}}, "calls[0]: укажите одно: command_name или command"},
		{"type error", map[string]any{"calls": []any{map[string]any{
			"server": "a", "command": 5,
		}}}, "calls[0]: ключ command: ожидался тип «строка», получен тип «число»"},
		{"bad parameters", map[string]any{"calls": []any{map[string]any{
			"server": "a", "command_name": "echo", "parameters": "abc",
		}}}, "parameters: ожидался объект"},
		{"negative maxBytes", map[string]any{
			"calls": []any{echoCall("a", "x", 0)}, "maxBytes": -5,
		}, "maxBytes: не может быть отрицательным"},
	}
	for _, tc := range cases {
		res := call(t, cs, "invoke_many", tc.args)
		if !res.IsError {
			t.Fatalf("%s: ожидалась ошибка", tc.name)
		}
		if text := resultText(t, res); !strings.Contains(text, tc.want) {
			t.Fatalf("%s: %q не содержит %q", tc.name, text, tc.want)
		}
	}
	if peak := f.peak("a"); peak != 0 {
		t.Fatalf("невалидный вход что-то выполнил: %d вызовов", peak)
	}
}

// list_commands называет поле command; invoke_many принимает его синонимом.
func TestInvokeManyCommandAlias(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, nil, "a")
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		map[string]any{"server": "a", "command": "echo", "parameters": map[string]any{"text": "one"}},
		map[string]any{"server": "a", "command": "echo", "parameters": map[string]any{"text": "two"}},
	}})
	if res.IsError {
		t.Fatalf("command должен приниматься как синоним command_name: %s", resultText(t, res))
	}
	text := resultText(t, res)
	if !strings.Contains(text, "a:one") || !strings.Contains(text, "a:two") {
		t.Fatalf("ответ не тот: %q", text)
	}
	if peak := f.peak("a"); peak == 0 {
		t.Fatal("принятый вызов не дошёл до сервера")
	}
}

// Отказ политики в веере несёт тот же маркер и Meta, что одиночный вызов:
// хук-клиент ищет их в result, а не в строке error.
func TestInvokeManyDenyKeepsPolicyShape(t *testing.T) {
	f := newFanTracker()
	cfg := &config.Config{Permissions: &config.Permissions{Rules: []config.Rule{
		{Pattern: "boom", Action: config.PermissionDeny, Message: "только чтение"},
	}}}
	m := fanManager(t, f, cfg, "a")
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		map[string]any{"server": "a", "command_name": "boom", "parameters": map[string]any{}},
		echoCall("a", "ok", 0),
	}})
	if res.IsError {
		t.Fatalf("веер упал: %s", resultText(t, res))
	}
	out := manyAnswers(t, res)
	if out[0].OK || !strings.Contains(out[0].Error, "🚫 Blocked by mcprig policy: только чтение") {
		t.Fatalf("calls[0] = %+v", out[0])
	}
	shape, ok := out[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("calls[0].result не объект: %#v", out[0].Result)
	}
	if meta, _ := shape["_meta"].(map[string]any); meta["lazyMcpPolicy"] != "blocked" {
		t.Fatalf("result._meta.lazyMcpPolicy: %#v", shape["_meta"])
	}
	var texts []string
	content, _ := shape["content"].([]any)
	for _, item := range content {
		if part, ok := item.(map[string]any); ok {
			if text, ok := part["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	if !strings.Contains(strings.Join(texts, "\n"), "🚫 Blocked by mcprig policy: только чтение") {
		t.Fatalf("в result.content нет маркера: %q", texts)
	}
	if !out[1].OK {
		t.Fatalf("соседний вызов не ok: %+v", out[1])
	}
	if peak := f.peak("a"); peak != 1 {
		t.Fatalf("отказ дошёл до сервера: %d вызовов", peak)
	}
}
