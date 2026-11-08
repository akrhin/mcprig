package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadSet(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestOAuthDefaultsAndOverride(t *testing.T) {
	cfg := loadSet(t, `{
	  "oauth": {"clientId": "shared", "scopes": ["mcp.read"]},
	  "servers": [
	    {"name": "plain", "url": "https://a.example/mcp"},
	    {"name": "own", "url": "https://b.example/mcp",
	     "oauth": {"clientId": "own-id", "callbackPort": 9000}}
	  ]}`)

	plain := cfg.OAuthFor(cfg.Servers[0])
	if plain.ClientID != "shared" || plain.CallbackPort != DefaultCallbackPort {
		t.Fatalf("plain: %+v", plain)
	}
	if plain.RedirectURL != "http://127.0.0.1:8947/callback" {
		t.Fatalf("redirect: %q", plain.RedirectURL)
	}
	if len(plain.Scopes) != 1 || plain.Scopes[0] != "mcp.read" {
		t.Fatalf("scopes: %v", plain.Scopes)
	}

	own := cfg.OAuthFor(cfg.Servers[1])
	if own.ClientID != "own-id" || own.CallbackPort != 9000 {
		t.Fatalf("own: %+v", own)
	}
	if own.RedirectURL != "http://127.0.0.1:9000/callback" {
		t.Fatalf("own redirect: %q", own.RedirectURL)
	}
}

func TestOAuthBadBlocksAreSoft(t *testing.T) {
	// A secret without a client id is a server-level defect: that server is out,
	// the rest of the set keeps working.
	cfg := loadSet(t, `{"servers": [
	  {"name": "bad", "url": "https://a.example/mcp", "oauth": {"clientSecret": "s"}},
	  {"name": "ok", "url": "https://b.example/mcp"}
	]}`)
	if cfg.Servers[0].ConfigError == "" {
		t.Fatal("ожидалась ошибка про clientSecret без clientId")
	}
	if got := cfg.Usable(); len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("usable: %v", got)
	}

	// A broken set-wide block is dropped, not fatal.
	cfg = loadSet(t, `{"oauth": {"callbackPort": -5}, "servers": [
	  {"name": "ok", "url": "https://b.example/mcp"}]}`)
	if cfg.OAuth == nil {
		return // dropped, as intended
	}
	t.Fatalf("глобальный блок должен быть отброшен: %+v", cfg.OAuth)
}

func TestOAuthDisabledIsInherited(t *testing.T) {
	cfg := loadSet(t, `{"oauth": {"disabled": true}, "servers": [
	  {"name": "a", "url": "https://a.example/mcp"}]}`)
	if !cfg.OAuthFor(cfg.Servers[0]).Disabled {
		t.Fatal("disabled должен наследоваться")
	}
}
