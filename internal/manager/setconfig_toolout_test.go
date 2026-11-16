package manager

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
)

// toolOutTtl в servers.json: набор под панелью не наследует окружение панели,
// поэтому срок жизни выгрузки доезжает только конфигом. Маркер обещает файл на
// этот срок, и панель чистит tool-out по нему же.
func TestBuildSetConfigCarriesToolOutTTL(t *testing.T) {
	t.Setenv("MCPRIG_TOOLOUT_TTL", "3h")
	set := &registry.Set{ID: "main", Port: 9101, Token: "tok-active", Servers: []string{"a"}}
	servers := []registry.Server{{Name: "a", Enabled: true, Command: []string{"x"}}}

	body, err := BuildSetConfig(set, servers, "0.0.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Transport struct {
			ToolOutTTL string `json:"toolOutTtl"`
		} `json:"transport"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	ttl, err := time.ParseDuration(out.Transport.ToolOutTTL)
	if err != nil || ttl != 3*time.Hour {
		t.Fatalf("toolOutTtl = %q, ожидалось 3h", out.Transport.ToolOutTTL)
	}
}
