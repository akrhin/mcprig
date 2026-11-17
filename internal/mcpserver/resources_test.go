package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// resourceUpstream is a downstream server carrying both surfaces the proxy can
// expose: two resources and one prompt. No network, no child process.
func resourceUpstream(ctx context.Context) (*mcp.ClientSession, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: "downstream", Version: "test"}, nil)
	bodies := map[string]string{
		"test://open/one":   "открытый ресурс",
		"test://secret/one": "секретные данные",
		"test://big":        strings.Repeat("я", 100_000),
	}
	for uri, body := range bodies {
		uri, body := uri, body
		s.AddResource(&mcp.Resource{URI: uri, Name: uri, MIMEType: "text/plain"}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: req.Params.URI, MIMEType: "text/plain", Text: body},
			}}, nil
		})
	}
	s.AddPrompt(&mcp.Prompt{
		Name:        "greet",
		Description: "приветствие",
		Arguments:   []*mcp.PromptArgument{{Name: "who", Required: true}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: "hello " + req.Params.Arguments["who"]},
		}}}, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		return nil, err
	}
	return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(context.Background(), ct, nil)
}

func resourceManager(t *testing.T, cfg *config.Config) *Manager {
	t.Helper()
	m := New(cfg, "test", nil)
	m.dial = func(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
		return resourceUpstream(ctx)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestResourceReadRequiresExpose: without expose the client sees no resource and
// a direct call is refused; with expose the same call returns the content.
func TestResourceReadRequiresExpose(t *testing.T) {
	closed := stdio("alpha")
	shut := resourceManager(t, &config.Config{Servers: []*config.Server{closed}})
	cs := clientFor(t, shut.Server())
	res := call(t, cs, "read_resource", map[string]any{"server": "alpha", "uri": "test://open/one"})
	if !res.IsError || !strings.Contains(resultText(t, res), "набор не отдаёт ресурсы") {
		t.Fatalf("без expose ожидался отказ, got %q", resultText(t, res))
	}

	open := stdio("alpha")
	open.Expose = []string{config.ExposeResources}
	m := resourceManager(t, &config.Config{Servers: []*config.Server{open}})
	res = call(t, clientFor(t, m.Server()), "read_resource", map[string]any{"server": "alpha", "uri": "test://open/one"})
	if res.IsError || !strings.Contains(resultText(t, res), "открытый ресурс") {
		t.Fatalf("с expose ожидалось содержимое, got %q", resultText(t, res))
	}
}

// TestPermissionsApplyToResources: a rule addresses a resource as resource:<uri>,
// so the ordinary vocabulary works and a neighbour URI still passes.
func TestPermissionsApplyToResources(t *testing.T) {
	srv := stdio("alpha")
	srv.Expose = []string{config.ExposeResources}
	cfg := &config.Config{
		Servers: []*config.Server{srv},
		Permissions: &config.Permissions{Rules: []config.Rule{
			{Pattern: "resource:test://secret/*", Action: config.PermissionDeny, Message: "секрет закрыт"},
		}},
	}
	m := resourceManager(t, cfg)
	cs := clientFor(t, m.Server())

	res := call(t, cs, "read_resource", map[string]any{"server": "alpha", "uri": "test://secret/one"})
	if !res.IsError || !strings.Contains(resultText(t, res), "🚫 Blocked by mcprig policy: секрет закрыт") {
		t.Fatalf("deny: got %q", resultText(t, res))
	}
	res = call(t, cs, "read_resource", map[string]any{"server": "alpha", "uri": "test://open/one"})
	if res.IsError || !strings.Contains(resultText(t, res), "открытый ресурс") {
		t.Fatalf("соседний uri должен проходить, got %q", resultText(t, res))
	}
}

// TestResourceTruncatedAndSpilled: a big resource is cut at the default cap and
// the whole one lands in a dump file named by the marker.
func TestResourceTruncatedAndSpilled(t *testing.T) {
	dir := t.TempDir()
	dumpDir(t, dir)
	srv := stdio("alpha")
	srv.Expose = []string{config.ExposeResources}
	m := resourceManager(t, &config.Config{Servers: []*config.Server{srv}})

	res := call(t, clientFor(t, m.Server()), "read_resource", map[string]any{"server": "alpha", "uri": "test://big"})
	// Ответ на большой ресурс — тоже JSON, поэтому усечение структурное.
	text := resultText(t, res)
	var out []any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("усечённый ресурс перестал быть JSON: %v", err)
	}
	last, _ := out[len(out)-1].(string)
	if !strings.Contains(last, "ответ усечён") {
		t.Fatalf("нет признака усечения: %q", text[:min(len(text), 200)])
	}
	path := dumpPath(t, text)
	if !strings.HasPrefix(path, dir) {
		t.Fatalf("дамп не в тестовом каталоге: %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("файл полного ответа: %v", err)
	}
	if info.Size() < 150_000 {
		t.Errorf("полный ответ короче ожидаемого: %d байт", info.Size())
	}
}

// TestPromptsListAndGet: the list names the prompt and its argument, and an
// argument reaches the upstream.
func TestPromptsListAndGet(t *testing.T) {
	srv := stdio("alpha")
	srv.Expose = []string{config.ExposePrompts}
	m := resourceManager(t, &config.Config{Servers: []*config.Server{srv}})
	cs := clientFor(t, m.Server())

	var listed []promptInfo
	if err := json.Unmarshal([]byte(resultText(t, call(t, cs, "list_prompts", map[string]any{"server": "alpha"}))), &listed); err != nil {
		t.Fatalf("list_prompts: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "greet" || len(listed[0].Arguments) != 1 || !listed[0].Arguments[0].Required {
		t.Fatalf("list_prompts: %+v", listed)
	}

	out := resultText(t, call(t, cs, "get_prompt", map[string]any{
		"server": "alpha", "name": "greet", "arguments": map[string]any{"who": "мир"},
	}))
	if !strings.Contains(out, "hello мир") {
		t.Fatalf("аргумент не дошёл до upstream: %q", out)
	}

	// A prompt is refused when the server exposes resources only.
	only := stdio("alpha")
	only.Expose = []string{config.ExposeResources}
	other := resourceManager(t, &config.Config{Servers: []*config.Server{only}})
	res := call(t, clientFor(t, other.Server()), "get_prompt", map[string]any{"server": "alpha", "name": "greet"})
	if !res.IsError || !strings.Contains(resultText(t, res), "набор не отдаёт промпты") {
		t.Fatalf("промпты без expose: %q", resultText(t, res))
	}
}

// TestResourceCallsAreRateLimited: the limit counts the meta-tool, so a burst of
// distinct URIs cannot slip past it one bucket each.
func TestResourceCallsAreRateLimited(t *testing.T) {
	srv := stdio("alpha")
	srv.Expose = []string{config.ExposeResources}
	cfg := &config.Config{
		Servers:   []*config.Server{srv},
		RateLimit: &config.RateLimit{PerMinute: 1, Burst: 1},
	}
	m := resourceManager(t, cfg)
	cs := clientFor(t, m.Server())

	if res := call(t, cs, "read_resource", map[string]any{"server": "alpha", "uri": "test://open/one"}); res.IsError {
		t.Fatalf("первый вызов: %q", resultText(t, res))
	}
	res := call(t, cs, "read_resource", map[string]any{"server": "alpha", "uri": "test://big"})
	if !res.IsError || !strings.Contains(resultText(t, res), "лимит") {
		t.Fatalf("второй вызов по другому uri должен быть отбит лимитом, got %q", resultText(t, res))
	}
}

// TestListResourcesAllServers: no server lists every exposing server, keyed by
// name; a server without expose is absent rather than empty.
func TestListResourcesAllServers(t *testing.T) {
	with := stdio("alpha")
	with.Expose = []string{config.ExposeResources}
	without := stdio("beta")
	m := resourceManager(t, &config.Config{Servers: []*config.Server{with, without}})

	var out map[string][]resourceInfo
	if err := json.Unmarshal([]byte(resultText(t, call(t, clientFor(t, m.Server()), "list_resources", nil))), &out); err != nil {
		t.Fatalf("list_resources: %v", err)
	}
	if len(out["alpha"]) != 3 || len(out["beta"]) != 0 {
		t.Fatalf("list_resources: %+v", out)
	}
}
