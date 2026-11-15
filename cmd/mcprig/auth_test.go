package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSet кладёт servers.json одного сервера в каталог набора.
func writeSet(t *testing.T, dataDir, setID, body string) {
	t.Helper()
	dir := filepath.Join(dataDir, "sets", setID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "servers.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAuthTakesSetDir(t *testing.T) {
	data := t.TempDir()
	writeSet(t, data, "main", `{"servers":[{"name":"swaggerhub","transport":"http","url":"https://example.test/mcp"}]}`)

	tg, err := resolveAuth("main", "", data, "swaggerhub")
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	want := filepath.Join(data, "sets", "main", "servers.json")
	if tg.CfgPath != want {
		t.Errorf("CfgPath = %q, хотим %q", tg.CfgPath, want)
	}
	// Хранилище токенов обязано быть в каталоге набора: иначе набор его не увидит.
	if tg.StoreDir != filepath.Join(data, "sets", "main") {
		t.Errorf("StoreDir = %q", tg.StoreDir)
	}
	if tg.Server.URL != "https://example.test/mcp" {
		t.Errorf("сервер = %+v", tg.Server)
	}
}

func TestResolveAuthConfigKeepsProcessDataDir(t *testing.T) {
	data := t.TempDir()
	cfg := filepath.Join(data, "servers.json")
	body := `{"servers":[{"name":"s","transport":"stdio","command":["true"]}]}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tg, err := resolveAuth("", cfg, data, "s")
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if tg.StoreDir != data {
		t.Errorf("StoreDir = %q, хотим %q", tg.StoreDir, data)
	}
}

func TestResolveAuthErrors(t *testing.T) {
	data := t.TempDir()
	writeSet(t, data, "main", `{"servers":[{"name":"a","transport":"http","url":"https://x"}]}`)

	cases := []struct {
		name, set, cfg, server, want string
	}{
		{"нет выбора", "", "", "a", "укажите набор"},
		{"оба флага", "main", "/tmp/x.json", "a", "укажите что-то одно"},
		{"нет набора", "nope", "", "a", "nope"},
		{"нет сервера", "main", "", "b", "не найден"},
	}
	for _, c := range cases {
		if _, err := resolveAuth(c.set, c.cfg, data, c.server); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, хотим упоминание %q", c.name, err, c.want)
		}
	}
}
