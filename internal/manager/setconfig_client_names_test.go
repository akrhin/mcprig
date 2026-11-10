package manager

import (
	"encoding/json"
	"testing"

	"github.com/sintez/mcprig/internal/registry"
	"github.com/sintez/mcprig/internal/stats"
)

// clientNames в servers.json: подпись ключа под отпечатком, чтобы набор
// подписывал клиента именем, а не «key:<fp>/<ua>». Токен в конфиг не уходит,
// ключ без токена и без подписи имени не даёт.
func TestBuildSetConfigCarriesClientNames(t *testing.T) {
	set := &registry.Set{ID: "main", Port: 9101, Token: "tok-active", Servers: []string{"a"}, Keys: []registry.Key{
		{ID: "k1", Token: "tok-active", Note: " hermes "},
		{ID: "k2", Token: "", Note: "без токена"},
		{ID: "k3", Token: "tok-old", Note: "   "},
	}}
	servers := []registry.Server{{Name: "a", Enabled: true, Command: []string{"x"}}}

	body, err := BuildSetConfig(set, servers, "0.0.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Transport struct {
			ClientNames map[string]string `json:"clientNames"`
		} `json:"transport"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	fp := stats.Fingerprint("tok-active")
	if len(out.Transport.ClientNames) != 1 || out.Transport.ClientNames[fp] != "hermes" {
		t.Fatalf("clientNames = %v, want {%s: hermes}", out.Transport.ClientNames, fp)
	}
	if fp != stats.Fingerprint("tok-active") || len(fp) != 6 {
		t.Fatalf("отпечаток %q", fp)
	}

	// Ни одной подписи — поля нет вовсе.
	set.Keys[0].Note = ""
	body, err = BuildSetConfig(set, servers, "0.0.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	out.Transport.ClientNames = nil
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Transport.ClientNames != nil {
		t.Fatalf("clientNames должны отсутствовать, пришли %v", out.Transport.ClientNames)
	}
}
