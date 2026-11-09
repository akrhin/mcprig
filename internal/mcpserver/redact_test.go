package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
)

// serverSecrets takes only credential-named fields, strips the bearer prefix
// and ignores values too short to be a key.
func TestServerSecrets(t *testing.T) {
	srv := &config.Server{
		Env: map[string]string{
			"TAVILY_API_KEY": "tvly-dev-9cJrE-5UpOd",
			"HOME":           "/home/x",
			"SHORT_TOKEN":    "abc",
		},
		Headers: map[string]string{
			"Authorization": "Bearer tok-abcdefghij",
			"Accept":        "application/json",
		},
	}
	got := serverSecrets(srv)
	want := map[string]bool{"tvly-dev-9cJrE-5UpOd": true, "tok-abcdefghij": true}
	if len(got) != len(want) {
		t.Fatalf("serverSecrets: want %d values, got %q", len(want), got)
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("unexpected secret %q", v)
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
