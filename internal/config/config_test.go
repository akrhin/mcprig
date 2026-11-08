package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadNormalizes(t *testing.T) {
	path := write(t, `{
	  "servers": [
	    {"name": "remote", "url": "https://example.test/mcp"},
	    {"name": "array", "command": ["npx", "-y", "pkg"]},
	    {"name": "legacy", "command": "uvx", "args": ["srv"]},
	    {"name": "off", "url": "https://x/mcp", "enabled": false}
	  ],
	  "transport": {"type": "http", "port": 9100, "authToken": "t"},
	  "requestTimeout": 120000
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Servers) != 4 {
		t.Fatalf("servers: want 4, got %d", len(cfg.Servers))
	}
	byName := map[string]*Server{}
	for _, s := range cfg.Servers {
		byName[s.Name] = s
	}
	if got := byName["remote"].Transport; got != TransportHTTP {
		t.Errorf("remote transport: want http (from url), got %q", got)
	}
	if got := byName["array"].Transport; got != TransportStdio {
		t.Errorf("array transport: want stdio (from command), got %q", got)
	}
	if got := byName["legacy"].Command; len(got) != 2 || got[0] != "uvx" || got[1] != "srv" {
		t.Errorf("legacy command: want [uvx srv], got %v", got)
	}
	if !byName["remote"].Enabled {
		t.Error("enabled must default to true")
	}
	if byName["off"].Enabled {
		t.Error("enabled:false must be kept")
	}
	if cfg.Transport.Port != 9100 || cfg.RequestTimeout != 120000 {
		t.Errorf("transport/requestTimeout not read: %+v %d", cfg.Transport, cfg.RequestTimeout)
	}
	if usable := cfg.Usable(); len(usable) != 3 {
		t.Errorf("Usable: want 3 (one disabled), got %d", len(usable))
	}
}

func TestSoftFail(t *testing.T) {
	path := write(t, `{
	  "servers": [
	    {"name": "bad-env", "command": ["x"], "env": {"A": 1}},
	    {"name": "no-cmd", "transport": "stdio"},
	    {"name": "no-url", "transport": "http"},
	    {"name": "weird", "transport": "grpc", "url": "https://x"},
	    {"name": "cwd-http", "url": "https://x", "cwd": "/tmp"},
	    {"name": "dup", "command": ["x"]},
	    {"name": "dup", "command": ["y"]}
	  ]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("soft failures must not fail the load: %v", err)
	}
	for i, s := range cfg.Servers {
		want := s.Name != "dup" || i == 6
		if want && s.ConfigError == "" {
			t.Errorf("%s (index %d): want a config error, got none", s.Name, i)
		}
	}
	if cfg.Servers[5].ConfigError != "" || cfg.Servers[6].ConfigError == "" {
		t.Error("duplicate name: only the second occurrence must carry a config error")
	}
	// Только первая запись "dup" валидна — остальные сломаны тем или иным способом.
	if len(cfg.Usable()) != 1 {
		t.Errorf("Usable: want 1 healthy, got %d", len(cfg.Usable()))
	}
}

func TestIdentifiableFailsLoad(t *testing.T) {
	path := write(t, `{"servers": [{"nameless": true}]}`)
	if _, err := Load(path); err == nil {
		t.Fatal("a server without a name must fail the load")
	}
	path = write(t, `{"servers": [{"name": "ok", "command": ["x"], "command2": 1}`)
	if _, err := Load(path); err == nil {
		t.Fatal("broken JSON must fail the load")
	}
}
