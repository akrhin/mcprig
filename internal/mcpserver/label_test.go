package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
)

// reqWithMeta builds a tool-call request carrying only _meta, as a stateless
// HTTP client does: no session, no initialize handshake.
func reqWithMeta(meta mcp.Meta) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: meta}}
}

// The label comes from the per-request _meta clientInfo when the caller sends
// one, and only then falls back to the transport label (key plus User-Agent)
// and finally to "unknown".
func TestLabelOf(t *testing.T) {
	meta := func(name, version string) mcp.Meta {
		return mcp.Meta{mcp.MetaKeyClientInfo: map[string]any{"name": name, "version": version}}
	}
	cases := []struct {
		name string
		ctx  context.Context
		req  *mcp.CallToolRequest
		want string
	}{
		{"meta with version", context.Background(), reqWithMeta(meta("opencode", "2.2.6")), "opencode/2.2.6"},
		{"meta without version", context.Background(), reqWithMeta(meta("mcprig", "")), "mcprig"},
		{
			"meta wins over transport label",
			stats.WithClient(context.Background(), "key:abc123/opencode"),
			reqWithMeta(meta("opencode", "2.2.6")),
			"opencode/2.2.6",
		},
		{
			"no meta keeps transport label",
			stats.WithClient(context.Background(), "key:abc123/opencode"),
			reqWithMeta(nil),
			"key:abc123/opencode",
		},
		{"nothing at all", context.Background(), reqWithMeta(nil), "unknown"},
		{"nil request", stats.WithClient(context.Background(), "key:abc123"), nil, "key:abc123"},
		{"nil request, no label", context.Background(), nil, "unknown"},
	}
	for _, c := range cases {
		if got := labelOf(c.ctx, c.req); got != c.want {
			t.Errorf("%s: labelOf = %q, want %q", c.name, got, c.want)
		}
	}
}

// The recorder shows no weight until a tool cache exists, and then the weight
// of the whole-set list_commands answer made from that cache — no downstream
// call is made for the count.
func TestPublishSchema(t *testing.T) {
	m := testManager(&config.Config{Servers: []*config.Server{stdio("alpha")}}, new(int32), 0)
	defer m.Close()
	rec := stats.NewRecorder("s", 0)
	m.Stats = rec

	if got := rec.Snapshot().Schema; got != nil {
		t.Fatalf("schema before the cache is warm = %+v, want nil", got)
	}

	cs := clientFor(t, m.Server())
	call(t, cs, "list_commands", map[string]any{"server": "alpha"})

	schema := rec.Snapshot().Schema
	if schema == nil {
		t.Fatal("schema after list_commands = nil, want a weight")
	}
	if schema.Tools != 3 {
		t.Errorf("tools = %d, want 3", schema.Tools)
	}
	if want := stats.TokensOf(schema.Bytes); schema.Tokens != want {
		t.Errorf("tokens = %d, want %d for %d bytes", schema.Tokens, want, schema.Bytes)
	}
	// The weight must describe the answer list_commands builds, not something
	// else of a similar size.
	want, err := json.Marshal(map[string][]commandInfo{
		"alpha": {
			{Server: "alpha", Name: "echo", Command: "echo"},
			{Server: "alpha", Name: "danger-zone", Command: "danger-zone"},
			{Server: "alpha", Name: "boom", Command: "boom"},
		},
	})
	if err != nil {
		t.Fatalf("marshal expectation: %v", err)
	}
	if schema.Bytes != int64(len(want)) {
		t.Errorf("schemaBytes = %d, want %d", schema.Bytes, len(want))
	}
}
