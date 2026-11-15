package manager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/registry"
	"github.com/akrhin/mcprig/internal/stats"
)

// servers.json несёт профили набора и профиль каждого ключа под отпечатком,
// а набор читает их обратно: иначе профиль не дошёл бы до процесса набора.
func TestBuildSetConfigCarriesProfiles(t *testing.T) {
	set := &registry.Set{
		ID: "main", Port: 9101, Token: "tok-active", Servers: []string{"a"},
		Profiles: map[string]*config.Profile{"reader": {Deny: []string{"*/*_write"}}},
		Keys: []registry.Key{
			{ID: "k1", Token: "tok-active", Profile: "reader"},
			{ID: "k2", Token: "tok-plain"},
		},
	}
	servers := []registry.Server{{Name: "a", Enabled: true, Command: []string{"x"}}}
	body, err := BuildSetConfig(set, servers, "0.0.0.0", nil)
	if err != nil {
		t.Fatalf("BuildSetConfig: %v", err)
	}

	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p := cfg.Profiles["reader"]; p == nil || len(p.Deny) != 1 {
		t.Fatalf("набор не прочитал профиль: %+v", cfg.Profiles)
	}
	if got := cfg.Transport.KeyProfiles[stats.Fingerprint("tok-active")]; got != "reader" {
		t.Fatalf("keyProfiles = %v", cfg.Transport.KeyProfiles)
	}
	// Ключ без профиля в карту не попадает — он не ограничен.
	if len(cfg.Transport.KeyProfiles) != 1 {
		t.Fatalf("ключ без профиля попал в карту: %v", cfg.Transport.KeyProfiles)
	}

	// Отброшенный при загрузке блок (см. TestLoadDropsBrokenProfiles) оставляет
	// ключ с именем профиля: набор стартует без профилей, и гейт запрещает всё.
	set.Profiles = nil
	body, err = BuildSetConfig(set, servers, "0.0.0.0", nil)
	if err != nil {
		t.Fatalf("BuildSetConfig: %v", err)
	}
	path = filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatalf("профили появились без блока: %+v", cfg.Profiles)
	}
	if _, ok := cfg.Transport.KeyProfiles[stats.Fingerprint("tok-active")]; !ok {
		t.Fatalf("ключ потерял имя профиля: %v", cfg.Transport.KeyProfiles)
	}
}
