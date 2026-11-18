package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
)

// serverSecrets is a pass-through to `config.Server.Secrets()`: one filter for
// the proxy and the panel. It strips the bearer prefix and drops values too
// short to be a key, so `SHORT_TOKEN` must not appear.
func TestServerSecrets(t *testing.T) {
	srv := &config.Server{
		Env: map[string]string{
			"TAVILY_API_KEY": "tvly-dev-9cJrE-5UpOd",
			"SHORT_TOKEN":    "abc",
		},
		Headers: map[string]string{
			"Authorization": "Bearer tok-abcdefghij",
		},
	}
	got := serverSecrets(srv)
	want := []string{"tvly-dev-9cJrE-5UpOd", "tok-abcdefghij"}
	if len(got) != len(want) {
		t.Fatalf("serverSecrets: want %d values, got %q", len(want), got)
	}
	for _, v := range want {
		if !slices.Contains(got, v) {
			t.Errorf("нет секрета %q в %q", v, got)
		}
	}
}

// An upstream that repeats its own key in the error text must not leak it to
// the caller — tavily does exactly that on a validation error.
func TestInvokeCommandMasksSecretInError(t *testing.T) {
	const secret = "tvly-dev-SECRETSECRET"
	leaky := func(ctx context.Context) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "leaky", Version: "test"}, nil)
		s.AddTool(&mcp.Tool{Name: "fail", InputSchema: objectSchema(map[string]any{})},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
					Text: `Tavily API error: {"api_key":"` + secret + `","path":"/home/x"}`,
				}}}, nil
			})
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(context.Background(), ct, nil)
	}

	srv := stdio("leaky")
	srv.Env = map[string]string{"TAVILY_API_KEY": secret, "HOME": "/home/x"}
	m := New(&config.Config{Servers: []*config.Server{srv}}, "test", nil)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) { return leaky(ctx) }
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{"server": "leaky", "command_name": "fail"})
	text := resultText(t, res)
	if strings.Contains(text, secret) {
		t.Fatalf("secret leaked into the tool result: %s", text)
	}
	if !strings.Contains(text, "***") {
		t.Errorf("want a masked placeholder, got %q", text)
	}
	if !strings.Contains(text, "/home/x") {
		t.Errorf("a non-secret env value must survive, got %q", text)
	}
}

// leakyProtocolError — апстрим отвечает JSON-RPC ошибкой (не isError-содержимым)
// с ключом в тексте: этот путь идёт в err, а не в res.
func leakyProtocolError(secret string) func(context.Context) (*mcp.ClientSession, error) {
	return func(ctx context.Context) (*mcp.ClientSession, error) {
		s := mcp.NewServer(&mcp.Implementation{Name: "leaky", Version: "test"}, nil)
		s.AddTool(&mcp.Tool{Name: "fail", InputSchema: objectSchema(map[string]any{})},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, fmt.Errorf("GITHUB rejected key %s", secret)
			})
		st, ct := mcp.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(context.Background(), ct, nil)
	}
}

// Текст JSON-RPC ошибки апстрима тоже повторяет ключ: err уходит в контекст
// модели через errResult, и маскируется он там же, где ответ.
func TestInvokeCommandMasksSecretInProtocolError(t *testing.T) {
	// Значение словами, а не hex: gitleaks принимает hex-строку рядом со
	// словом secret за ключ и валит job `secrets` на поддельном ключе.
	const secret = "supersecret-token-value"
	srv := stdio("leaky")
	srv.Env = map[string]string{"GITHUB_TOKEN": secret}
	m := New(&config.Config{Servers: []*config.Server{srv}}, "test", nil)
	m.Stats = stats.NewRecorder("test", 8)
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		return leakyProtocolError(secret)(ctx)
	}
	defer m.Close()
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{"server": "leaky", "command_name": "fail"})
	text := resultText(t, res)
	if strings.Contains(text, secret) {
		t.Fatalf("secret leaked into the tool result: %s", text)
	}
	if !strings.Contains(text, "***") {
		t.Errorf("want a masked placeholder, got %q", text)
	}
	// Любая записанная строка — без секрета.
	for _, c := range m.Stats.Snapshot().Tail {
		if strings.Contains(c.Message, secret) {
			t.Fatalf("secret leaked into stats: %q", c.Message)
		}
	}
}

// Мета-вызов при падающем апстриме пишет текст его ошибки в счётчики: ключ из
// этой ошибки маскируется секретами всего набора.
func TestMetaStatsMaskUpstreamSecret(t *testing.T) {
	// Значение словами, а не hex: gitleaks принимает hex-строку рядом со
	// словом secret за ключ и валит job `secrets` на поддельном ключе.
	const secret = "supersecret-token-value"
	srv := stdio("alpha")
	srv.Env = map[string]string{"GITHUB_TOKEN": secret}
	m := New(&config.Config{Servers: []*config.Server{srv}}, "test", nil)
	m.Stats = stats.NewRecorder("test", 8)
	m.dial = func(context.Context, *config.Server) (*mcp.ClientSession, error) {
		return nil, fmt.Errorf("dial: key %s rejected", secret)
	}
	defer m.Close()
	cs := clientFor(t, m.Server())

	if res := call(t, cs, "list_commands", map[string]any{"server": "alpha"}); !res.IsError {
		t.Fatalf("падающий апстрим не дал ошибку: %s", resultText(t, res))
	}
	snap := m.Stats.Snapshot()
	if len(snap.Tail) != 1 || !snap.Tail[0].Failed {
		t.Fatalf("мета-вызов не записан: %+v", snap.Tail)
	}
	if msg := snap.Tail[0].Message; strings.Contains(msg, secret) || !strings.Contains(msg, "***") {
		t.Fatalf("в счётчиках секрет: %q", msg)
	}
}
