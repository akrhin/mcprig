package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestProxyServesMetaTools is the stage 1 gate: a real binary, a real stdio
// downstream process, the four meta-tools over HTTP.
func TestProxyServesMetaTools(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "testmcp")
	bin := filepath.Join(dir, "mcprig")
	for src, out := range map[string]string{"./cmd/testmcp": fixture, "./cmd/mcprig": bin} {
		cmd := exec.Command("go", "build", "-o", out, src)
		cmd.Dir = ".."
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", src, err, b)
		}
	}

	port := freePort(t)
	cfg := filepath.Join(dir, "servers.json")
	body := fmt.Sprintf(`{"transport":{"type":"http","host":"127.0.0.1","port":%d,"path":"/mcp","authToken":"tkn"},
		"servers":[{"name":"fx","description":"fixture","command":[%q]}]}`, port, fixture)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	proxy := exec.Command(bin, "-config", cfg)
	proxy.Stderr = os.Stderr
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = proxy.Process.Signal(syscall.SIGTERM)
		_, _ = proxy.Process.Wait()
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitHealthy(t, base)

	c := &mcpClient{t: t, url: base + "/mcp", token: "tkn"}
	c.post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}`)

	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	c.decode(c.post(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`), &list)
	got := map[string]bool{}
	for _, tool := range list.Result.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"list_servers", "list_commands", "describe_commands", "invoke_command"} {
		if !got[want] {
			t.Errorf("в tools/list нет %q, есть %v", want, keys(got))
		}
	}

	servers := c.toolText("list_servers", map[string]any{})
	var state struct {
		Servers []struct {
			Name    string `json:"name"`
			Healthy bool   `json:"healthy"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(servers), &state); err != nil {
		t.Fatalf("list_servers не JSON: %v (%s)", err, servers)
	}
	var fxHealthy bool
	for _, s := range state.Servers {
		if s.Name == "fx" && s.Healthy {
			fxHealthy = true
		}
	}
	if !fxHealthy {
		t.Errorf("list_servers не показал живой фикстурный сервер: %s", servers)
	}

	echo := c.toolText("invoke_command", map[string]any{
		"server": "fx", "command_name": "echo", "parameters": map[string]any{"text": "привет"},
	})
	if !strings.Contains(echo, "привет") {
		t.Errorf("invoke_command echo вернул %q", echo)
	}

	fail := c.toolText("invoke_command", map[string]any{
		"server": "fx", "command_name": "fail", "parameters": map[string]any{},
	})
	if !strings.Contains(fail, "fixture failure") {
		t.Errorf("ошибка downstream не дошла до клиента: %q", fail)
	}

	if escaped := c.toolText("invoke_command", map[string]any{
		"server": "nope", "command_name": "echo", "parameters": map[string]any{},
	}); !strings.Contains(escaped, "nope") {
		t.Errorf("неизвестный сервер не дал внятной ошибки: %q", escaped)
	}
}

type mcpClient struct {
	t     *testing.T
	url   string
	token string
}

func (c *mcpClient) post(payload string) map[string]any {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader([]byte(payload)))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		// The streamable transport may answer in SSE frames.
		out = nil
		for _, line := range strings.Split(string(raw), "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				if json.Unmarshal([]byte(data), &out) == nil && out != nil {
					break
				}
			}
		}
		if out == nil {
			c.t.Fatalf("ответ не JSON (статус %d): %s", res.StatusCode, raw)
		}
	}
	return out
}

func (c *mcpClient) decode(in map[string]any, out any) {
	c.t.Helper()
	raw, _ := json.Marshal(in)
	if err := json.Unmarshal(raw, out); err != nil {
		c.t.Fatalf("декодирование ответа: %v (%s)", err, raw)
	}
}

// toolText calls a meta-tool and concatenates its text content.
func (c *mcpClient) toolText(name string, args map[string]any) string {
	c.t.Helper()
	res := c.post(fmt.Sprintf(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
		name, mustJSON(args)))
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	c.decode(res, &out)
	if out.Error != nil {
		c.t.Fatalf("%s: ошибка протокола: %s", name, out.Error.Message)
	}
	var b strings.Builder
	for _, part := range out.Result.Content {
		b.WriteString(part.Text)
	}
	return b.String()
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(base + "/healthz")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("прокси не поднялся за 15 с")
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
