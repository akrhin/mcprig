package manager

import (
	"encoding/json"
	"testing"

	"github.com/akrhin/mcprig/internal/registry"
)

// Разрешённые Host попадают в конфиг набора: иначе набор, поднятый панелью
// с MCPRIG_BIND_IP=0.0.0.0, отвечает «forbidden host» на адрес из локальной сети.
func TestBuildSetConfigCarriesAllowedHosts(t *testing.T) {
	set := &registry.Set{ID: "main", Port: 9101, Token: "tok", Servers: []string{"a"}}
	servers := []registry.Server{{Name: "a", Enabled: true, Command: []string{"x"}}}

	body, err := BuildSetConfig(set, servers, "0.0.0.0", []string{"192.168.1.92", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Transport struct {
			Host         string   `json:"host"`
			AllowedHosts []string `json:"allowedHosts"`
		} `json:"transport"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Transport.Host != "0.0.0.0" {
		t.Fatalf("host %q", out.Transport.Host)
	}
	if len(out.Transport.AllowedHosts) != 2 || out.Transport.AllowedHosts[0] != "192.168.1.92" {
		t.Fatalf("allowedHosts %v", out.Transport.AllowedHosts)
	}

	// Пустой список остаётся пустым: тогда набор слушает только loopback-имена.
	body, err = BuildSetConfig(set, servers, "0.0.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	out.Transport.AllowedHosts = nil
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Transport.AllowedHosts != nil {
		t.Fatalf("allowedHosts должны отсутствовать, пришли %v", out.Transport.AllowedHosts)
	}
}
