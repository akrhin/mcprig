package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
		// --key без --remote молча игнорировался — теперь ошибка.
		{"--client", "opencode", "--config", "x.json", "--key", "k123"},
		{"--client", "opencode", "--config", "x.json", "--key-file", "/tmp/k"},
		{"--remote", "http://127.0.0.1:9100/mcp", "--key", "a", "--key-file", "/tmp/k"},
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
	var doc map[string]map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("не JSON: %v\n%s", err, out)
	}
	entry := doc["mcp"]["servers"]["mcprig-demo"]
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

// Ключ читается из файла и из env: значение в argv видно в ps и истории.
func TestInitKeyFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	if err := os.WriteFile(keyFile, []byte("file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := "http://127.0.0.1:9100/mcp"

	out := captureStdout(t, func() {
		if err := runInit([]string{"--client", "claude", "--remote", remote, "--key-file", keyFile}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Bearer file-key") {
		t.Fatalf("ключ из файла не попал в конфиг: %s", out)
	}

	t.Setenv("MCPRIG_KEY", "env-key")
	out = captureStdout(t, func() {
		if err := runInit([]string{"--client", "claude", "--remote", remote}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Bearer env-key") {
		t.Fatalf("ключ из MCPRIG_KEY не попал в конфиг: %s", out)
	}
}

// --key - читает ключ из stdin, а не из argv.
func TestInitKeyFromStdin(t *testing.T) {
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()
	go func() { io.WriteString(w, "stdin-key\n"); w.Close() }()

	out := captureStdout(t, func() {
		if err := runInit([]string{"--client", "claude", "--remote", "http://127.0.0.1:9100/mcp", "--key", "-"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Bearer stdin-key") {
		t.Fatalf("ключ из stdin не попал в конфиг: %s", out)
	}
}
