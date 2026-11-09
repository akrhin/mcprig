package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestStdioModeIsClientOwned is the install-mode gate: the client spawns
// `mcprig stdio`, speaks MCP over stdin/stdout, and needs no port or key.
func TestStdioModeIsClientOwned(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "testmcp")
	bin := filepath.Join(dir, "mcprig")
	for src, out := range map[string]string{"../cmd/testmcp": fixture, "../cmd/mcprig": bin} {
		cmd := exec.Command("go", "build", "-o", out, src)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", src, err, b)
		}
	}

	cfg := filepath.Join(dir, "servers.json")
	body := fmt.Sprintf(`{"servers":[{"name":"fx","command":[%q]}]}`, fixture)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "stdio", "--config", cfg)
	cmd.Env = append(os.Environ(), "MCPRIG_DATA_DIR="+filepath.Join(dir, "state"))
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	write := func(v string) {
		if _, err := stdin.Write([]byte(v + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	read := func(wantID float64) map[string]any {
		for sc.Scan() {
			var msg map[string]any
			if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
				continue
			}
			if id, ok := msg["id"].(float64); ok && id == wantID {
				return msg
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("read: %v", err)
		}
		t.Fatalf("stdout closed before a reply to id %v", wantID)
		return nil
	}

	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}`)
	if init := read(1); init["result"] == nil {
		t.Fatalf("initialize: %v", init)
	}
	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)

	msg := read(2)
	result, _ := msg["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("ожидалось 5 мета-тулов, получено %d: %v", len(tools), result)
	}
}
