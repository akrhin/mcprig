package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout перехватывает вывод runInit: он печатает фрагмент конфига.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

func TestInitRejectsIncompleteArgs(t *testing.T) {
	cases := [][]string{
		{"--client", "vscode", "--config", "x.json"},
		{"--remote", "http://127.0.0.1:9100/mcp"},
		{"--client", "opencode"},
	}
	for _, args := range cases {
		if err := runInit(args); err == nil {
			t.Fatalf("%v: ожидалась ошибка использования", args)
		}
	}
}

func TestInitOpencodeLocalIsJSON(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runInit([]string{"--client", "opencode", "--config", "/tmp/servers.json"}); err != nil {
			t.Fatal(err)
		}
	})
	var doc map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("не JSON: %v\n%s", err, out)
	}
	entry := doc["mcp"]["mcprig-demo"]
	if entry["type"] != "local" {
		t.Fatalf("тип не local: %v", entry)
	}
	cmd, _ := entry["command"].([]any)
	if len(cmd) != 4 || cmd[1] != "stdio" {
		t.Fatalf("команда stdio собрана неверно: %v", cmd)
	}
}

func TestInitRemoteCarriesBearer(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runInit([]string{"--client", "claude", "--remote", "http://127.0.0.1:9100/mcp", "--key", "k123"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Bearer k123") || !strings.Contains(out, `"mcpServers"`) {
		t.Fatalf("нет bearer или ключа mcpServers: %s", out)
	}
}

func TestInitCodexIsTOML(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runInit([]string{"--client", "codex", "--config", "/tmp/servers.json"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "[mcp_servers.mcprig-demo]") || !strings.Contains(out, `command = "mcprig"`) {
		t.Fatalf("не TOML codex: %s", out)
	}
	if strings.Contains(out, "{") {
		t.Fatalf("codex получил JSON: %s", out)
	}
}
