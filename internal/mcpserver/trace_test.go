package mcpserver

// Ворота ставки E: traceparent проверяется, пробрасывается один, свои спаны
// пишутся без сети и без секретов.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/akrhin/mcprig/internal/config"
)

const clientTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// metaRecorder — апстрим, запоминающий _meta каждого вызова: только так видно,
// что именно ушло вниз.
type metaRecorder struct {
	mu    sync.Mutex
	metas []mcp.Meta
}

func (r *metaRecorder) add(meta mcp.Meta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metas = append(r.metas, meta)
}

func (r *metaRecorder) last(t *testing.T) mcp.Meta {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.metas) == 0 {
		t.Fatal("апстрим не получил ни одного вызова")
	}
	return r.metas[len(r.metas)-1]
}

// metaManager — менеджер с одним сервером, чей апстрим записывает _meta.
func metaManager(t *testing.T, srv *config.Server) (*Manager, *mcp.ClientSession, *metaRecorder) {
	t.Helper()
	rec := &metaRecorder{}
	m := New(&config.Config{Servers: []*config.Server{srv}}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "downstream", Version: "test"}, nil)
		s.AddTool(&mcp.Tool{Name: "echo", InputSchema: objectSchema(map[string]any{})},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				rec.add(req.Params.Meta)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
			})
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "mcprig", Version: "test"}, nil).Connect(ctx, ct, nil)
	}
	cs := clientFor(t, m.Server())
	t.Cleanup(func() { _ = m.Close() })
	return m, cs, rec
}

// recorderProvider подменяет глобальный провайдер записывающим в память: спаны
// проверяются без экспортера и без сети.
func recorderProvider(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

func spansNamed(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

func TestTraceparentValidated(t *testing.T) {
	cases := []struct {
		name string
		tp   string
		ok   bool
	}{
		{"валидный", clientTraceparent, true},
		{"без сэмплинга", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00", true},
		{"битый hex", "00-4bf92f3577b34da6a3ce929d0e0e473g-00f067aa0ba902b7-01", false},
		{"верхний регистр", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01", false},
		{"нулевой trace-id", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", false},
		{"нулевой parent-id", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", false},
		{"версия 01", "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", false},
		{"флаги ff", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-ff", false},
		{"пустая строка", "", false},
		{"просто мусор", "traceparent", false},
	}
	for _, c := range cases {
		sc, ok := spanContextOf(c.tp)
		if ok != c.ok {
			t.Fatalf("%s: ok=%v, ждали %v", c.name, ok, c.ok)
		}
		if ok && sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("%s: trace-id %s", c.name, sc.TraceID())
		}
	}

	// Битый traceparent не делает вызов ошибочным: клиент теряет трассу, а не
	// результат. Свой спан при этом стартует корневым — собственная трасса
	// прокси, а не продолжение мусора.
	recorderProvider(t)
	_, cs, up := metaManager(t, stdio("alpha"))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "invoke_command",
		Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{}},
		Meta:      mcp.Meta{"traceparent": "00-битый"},
	})
	if err != nil || res.IsError {
		t.Fatalf("битый traceparent сломал вызов: err=%v res=%v", err, res)
	}
	tp, _ := up.last(t)["traceparent"].(string)
	sc, ok := spanContextOf(tp)
	if !ok || sc.TraceID().String() == "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("битый traceparent дошёл до апстрима: %q", tp)
	}
}

func TestTraceparentReachesUpstream(t *testing.T) {
	rec := recorderProvider(t)
	_, cs, up := metaManager(t, stdio("alpha"))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "invoke_command",
		Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{}},
		Meta:      mcp.Meta{"traceparent": clientTraceparent},
	})
	if err != nil || res.IsError {
		t.Fatalf("вызов: err=%v res=%v", err, res)
	}

	tp, _ := up.last(t)["traceparent"].(string)
	down, ok := spanContextOf(tp)
	if !ok {
		t.Fatalf("апстрим не увидел валидный traceparent: %q", tp)
	}
	if got := down.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace-id родителя не сохранён: %s", got)
	}
	if down.SpanID().String() == "00f067aa0ba902b7" {
		t.Fatal("вниз ушёл span-id клиента: своего спана не появилось")
	}

	spans := spansNamed(rec, toolCallSpan)
	if len(spans) != 1 {
		t.Fatalf("спанов %s: %d, ждали 1", toolCallSpan, len(spans))
	}
	span := spans[0]
	if span.SpanContext().SpanID() != down.SpanID() {
		t.Fatalf("вниз ушёл span-id %s, а спан %s", down.SpanID(), span.SpanContext().SpanID())
	}
	if span.Parent().SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("родитель спана %s, ждали span-id клиента", span.Parent().SpanID())
	}
	want := map[string]string{
		"mcp.tool.name":    "invoke_command",
		"mcp.server.name":  "alpha",
		"mcp.command.name": "echo",
		"mcp.tool.outcome": "ok",
		"mcp.trace.source": "client",
	}
	for _, kv := range span.Attributes() {
		if v, ok := want[string(kv.Key)]; ok && kv.Value.AsString() != v {
			t.Fatalf("%s = %q, ждали %q", kv.Key, kv.Value.AsString(), v)
		}
	}
}

func TestMetaNotForwardedWholesale(t *testing.T) {
	_, cs, up := metaManager(t, stdio("alpha"))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "invoke_command",
		Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{}},
		Meta: mcp.Meta{
			"traceparent": clientTraceparent,
			"secret":      "клиентское _meta не для апстрима",
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("вызов: err=%v res=%v", err, res)
	}

	meta := up.last(t)
	if _, ok := meta["secret"]; ok {
		t.Fatal("клиентский ключ _meta ушёл вниз")
	}
	for k, v := range meta {
		if s, ok := v.(string); ok && strings.Contains(s, "клиентское") {
			t.Fatalf("значение клиентского _meta ушло вниз под ключом %q", k)
		}
	}
	if _, ok := meta["traceparent"]; !ok {
		t.Fatal("traceparent не дошёл до апстрима")
	}
}

func TestNoExporterMeansNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	sentinel := sdktrace.NewTracerProvider()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sentinel)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	shutdown, err := SetupTracing(context.Background())
	if err != nil {
		t.Fatalf("SetupTracing без env: %v", err)
	}
	if otel.GetTracerProvider() != sentinel {
		t.Fatal("без OTEL_EXPORTER_OTLP_ENDPOINT провайдер подменён")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// Спаны при этом пишутся — их просто некуда экспортировать.
	rec := recorderProvider(t)
	_, cs, up := metaManager(t, stdio("alpha"))
	if res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "invoke_command",
		Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{}},
	}); err != nil || res.IsError {
		t.Fatalf("вызов: err=%v res=%v", err, res)
	}
	if got := len(spansNamed(rec, toolCallSpan)); got != 1 {
		t.Fatalf("спанов в памяти %d, ждали 1", got)
	}
	tp, _ := up.last(t)["traceparent"].(string)
	if sc, ok := spanContextOf(tp); !ok || !sc.IsValid() {
		t.Fatalf("без клиентской трассы traceparent не выдан: %q", tp)
	}
}

func TestSpanAttributesHaveNoSecrets(t *testing.T) {
	const secret = "sk-live-0123456789abcdef"
	srv := stdio("alpha")
	srv.Env = map[string]string{"API_KEY": secret}
	rec := recorderProvider(t)
	_, cs, _ := metaManager(t, srv)

	if res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "invoke_command",
		Arguments: map[string]any{"server": "alpha", "command_name": "echo", "parameters": map[string]any{}},
		Meta:      mcp.Meta{"traceparent": clientTraceparent},
	}); err != nil || res.IsError {
		t.Fatalf("вызов: err=%v res=%v", err, res)
	}

	allowed := map[string]bool{
		"mcp.tool.name":            true,
		"mcp.tool.outcome":         true,
		"mcp.trace.source":         true,
		"mcp.server.name":          true,
		"mcp.command.name":         true,
		"mcprig.reload.generation": true,
		"mcprig.reload.applied":    true,
	}
	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("спаны не записаны")
	}
	for _, span := range spans {
		if strings.Contains(span.Name(), secret) {
			t.Fatalf("секрет в имени спана %q", span.Name())
		}
		check := func(k attribute.Key, v attribute.Value) {
			if strings.Contains(v.AsString(), secret) {
				t.Fatalf("секрет в атрибуте %q спана %q", k, span.Name())
			}
			if !allowed[string(k)] {
				t.Fatalf("нежданный атрибут %q спана %q", k, span.Name())
			}
		}
		for _, kv := range span.Attributes() {
			check(kv.Key, kv.Value)
		}
		for _, event := range span.Events() {
			for _, kv := range event.Attributes {
				if strings.Contains(kv.Value.AsString(), secret) {
					t.Fatalf("секрет в событии %q спана %q", event.Name, span.Name())
				}
			}
		}
	}
}
