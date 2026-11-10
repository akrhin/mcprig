package registry_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/manager"
	"github.com/sintez/mcprig/internal/registry"
)

// setConfigOf собирает конфиг набора и читает его так же, как это сделает
// запущенный набор: только этот путь доказывает, что поле доехало.
func setConfigOf(t *testing.T, store *registry.Store, set *registry.Set, servers ...registry.Server) *config.Config {
	t.Helper()
	body, err := manager.BuildSetConfig(set, servers, "127.0.0.1", nil)
	if err != nil {
		t.Fatalf("BuildSetConfig: %v", err)
	}
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("серверов в наборе: %d", len(cfg.Servers))
	}
	return cfg
}

func TestSandboxSurvivesRegistryRoundTrip(t *testing.T) {
	store, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := store.AddServer(json.RawMessage(`{
		"name": "boxed",
		"command": ["node", "server.js"],
		"tags": ["untrusted"],
		"envAllow": ["GITHUB_TOKEN"],
		"sandbox": {"mode": "bwrap", "network": "allow", "memoryMb": 2048, "ro": ["/opt/tool"]}
	}`))
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	set, err := store.AddSet(json.RawMessage(`{"name": "main", "servers": ["boxed"]}`))
	if err != nil {
		t.Fatalf("AddSet: %v", err)
	}

	cfg := setConfigOf(t, store, set, srv)
	got := cfg.Servers[0]
	if got.Sandbox == nil {
		t.Fatal("sandbox потерялся по дороге в набор")
	}
	if got.Sandbox.Mode != config.SandboxModeBwrap || got.Sandbox.Network != config.SandboxNetworkAllow {
		t.Errorf("песочница не та: %+v", *got.Sandbox)
	}
	if got.Sandbox.MemoryMb != 2048 {
		t.Errorf("memoryMb = %d", got.Sandbox.MemoryMb)
	}
	if !slices.Contains(got.Sandbox.RO, "/opt/tool") {
		t.Errorf("ro не доехал: %v", got.Sandbox.RO)
	}
	if !slices.Equal(got.EnvAllow, []string{"GITHUB_TOKEN"}) {
		t.Errorf("envAllow = %v", got.EnvAllow)
	}
	if got.ConfigError != "" {
		t.Errorf("набор отверг сервер: %s", got.ConfigError)
	}
}

func TestSandboxSurvivesDescriptionEdit(t *testing.T) {
	store, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name": "boxed", "command": ["node", "server.js"], "tags": ["untrusted"], "sandbox": {"memoryMb": 1024}}`
	if _, err := store.AddServer(json.RawMessage(body)); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	srv, err := store.UpdateServer("boxed", json.RawMessage(`{"description": "новое описание"}`))
	if err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	if srv.Sandbox == nil || srv.Sandbox.MemoryMb != 1024 {
		t.Fatalf("правка описания снесла песочницу: %+v", srv.Sandbox)
	}
	set, err := store.AddSet(json.RawMessage(`{"name": "main", "servers": ["boxed"]}`))
	if err != nil {
		t.Fatalf("AddSet: %v", err)
	}
	cfg := setConfigOf(t, store, set, srv)
	if cfg.Servers[0].Sandbox == nil || cfg.Servers[0].Sandbox.MemoryMb != 1024 {
		t.Errorf("песочница не доехала после правки: %+v", cfg.Servers[0].Sandbox)
	}
}

func TestRegisteredUntrustedNeedsSandbox(t *testing.T) {
	store, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddServer(json.RawMessage(`{"name": "naked", "command": ["node", "server.js"], "tags": ["untrusted"]}`))
	if err == nil {
		t.Fatal("сервер с untrusted без песочницы принят")
	}
	if !strings.Contains(err.Error(), "untrusted требует sandbox") {
		t.Errorf("причина не та: %v", err)
	}
	// Тот же запрет при правке: тег добавляют к уже сохранённому серверу.
	if _, err := store.AddServer(json.RawMessage(`{"name": "plain", "command": ["node", "server.js"]}`)); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	_, err = store.UpdateServer("plain", json.RawMessage(`{"tags": ["untrusted"]}`))
	if err == nil || !strings.Contains(err.Error(), "untrusted требует sandbox") {
		t.Fatalf("правка тега без песочницы прошла: %v", err)
	}
	// warn превращает отказ в тихий старт без изоляции — для untrusted нельзя.
	_, err = store.AddServer(json.RawMessage(`{"name": "loud", "command": ["node", "server.js"], "tags": ["untrusted"],
		"sandbox": {"onUnavailable": "warn"}}`))
	if err == nil || !strings.Contains(err.Error(), "onUnavailable") {
		t.Fatalf("onUnavailable=warn принят для untrusted: %v", err)
	}
}

func TestExportSetKeepsSandbox(t *testing.T) {
	store, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddServer(json.RawMessage(`{"name": "boxed", "command": ["node", "server.js"], "tags": ["untrusted"],
		"envAllow": ["TOKEN"], "sandbox": {"pids": 64}}`)); err != nil {
		t.Fatal(err)
	}
	set, err := store.AddSet(json.RawMessage(`{"name": "main", "servers": ["boxed"]}`))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.ExportSet(set.ID)
	if err != nil {
		t.Fatalf("ExportSet: %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	entry, _ := servers["boxed"].(map[string]any)
	if entry["sandbox"] == nil {
		t.Error("экспорт потерял sandbox")
	}
	if entry["envAllow"] == nil {
		t.Error("экспорт потерял envAllow")
	}
}
