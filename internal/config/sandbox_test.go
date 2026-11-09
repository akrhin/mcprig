package config

import (
	"strings"
	"testing"
	"time"
)

func TestSandboxDefaults(t *testing.T) {
	path := write(t, `{
	  "servers": [
	    {"name": "boxed", "command": ["node", "srv.js"], "sandbox": {}},
	    {"name": "loose", "command": ["node", "srv.js"], "sandbox": {"mode": "none"}}
	  ]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	boxed := cfg.Servers[0]
	if boxed.Sandbox == nil {
		t.Fatal("sandbox потерялся")
	}
	if boxed.Sandbox.Mode != SandboxModeBwrap || boxed.Sandbox.OnUnavailable != OnUnavailableDeny ||
		boxed.Sandbox.Network != SandboxNetworkNone {
		t.Errorf("умолчания блока: %+v", boxed.Sandbox)
	}
	if boxed.Sandbox.MemoryMb != DefaultSandboxMemoryMb || boxed.Sandbox.Pids != DefaultSandboxPids ||
		boxed.Sandbox.TimeoutSec != DefaultSandboxTimeout {
		t.Errorf("числовые умолчания: %+v", boxed.Sandbox)
	}
	if len(boxed.Sandbox.RO) != len(DefaultSandboxRO) {
		t.Errorf("ro по умолчанию: %v", boxed.Sandbox.RO)
	}
	if got := boxed.CallTimeout(); got != DefaultSandboxTimeout*time.Second {
		t.Errorf("CallTimeout: %v", got)
	}
	if got := cfg.Servers[1].CallTimeout(); got != 0 {
		t.Errorf("mode none не должен давать таймаут: %v", got)
	}
}

func TestSandboxRejectsFlagsAsPaths(t *testing.T) {
	path := write(t, `{
	  "servers": [{"name": "boxed", "command": ["x"], "sandbox": {"ro": ["--dev-bind"]}}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if msg := cfg.Servers[0].ConfigError; !strings.Contains(msg, "абсолютный путь") {
		t.Fatalf("блок с путём-флагом не отбракован: %q", msg)
	}
}

func TestSandboxRejectsUnknownEnums(t *testing.T) {
	for _, body := range []string{
		`"sandbox": {"mode": "seccomp"}`,
		`"sandbox": {"network": "host"}`,
		`"sandbox": {"onUnavailable": "maybe"}`,
		`"sandbox": {"memoryMb": -1}`,
	} {
		path := write(t, `{"servers": [{"name": "boxed", "command": ["x"], `+body+`}]}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Servers[0].ConfigError == "" {
			t.Errorf("%s: блок принят как валидный", body)
		}
	}
}

func TestSandboxOnlyForStdio(t *testing.T) {
	path := write(t, `{"servers": [{"name": "web", "url": "https://x/mcp", "sandbox": {}}]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if msg := cfg.Servers[0].ConfigError; !strings.Contains(msg, "stdio") {
		t.Fatalf("песочница на http не отбракована: %q", msg)
	}
}

func TestUntrustedNeedsSandbox(t *testing.T) {
	path := write(t, `{
	  "servers": [
	    {"name": "bare", "command": ["node", "srv.js"], "tags": ["Untrusted"]},
	    {"name": "no-mode", "command": ["node", "srv.js"], "tags": ["untrusted"], "sandbox": {"mode": "none"}},
	    {"name": "ok", "command": ["node", "srv.js"], "tags": ["untrusted"], "sandbox": {}}
	  ]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, srv := range cfg.Servers[:2] {
		if !strings.Contains(srv.ConfigError, "untrusted") {
			t.Errorf("%s: сервер без песочницы не помечен недоступным: %q", srv.Name, srv.ConfigError)
		}
	}
	if cfg.Servers[2].ConfigError != "" {
		t.Errorf("сервер в песочнице отбракован: %q", cfg.Servers[2].ConfigError)
	}
}

func TestWarningsUnpinnedRunner(t *testing.T) {
	cases := []struct {
		cmd  []string
		want bool
	}{
		{[]string{"npx", "-y", "@scope/pkg"}, true}, // без версии
		{[]string{"npx", "-y", "@scope/pkg@1.2.3"}, false},
		{[]string{"npx", "pkg@1.2.3"}, false},
		{[]string{"uvx", "srv"}, true},
		{[]string{"uvx", "srv==1.2"}, false},
		{[]string{"node", "srv.js"}, false},
	}
	for _, c := range cases {
		got := (&Server{Command: c.cmd}).Warnings()
		if (len(got) > 0) != c.want {
			t.Errorf("%v: warnings=%v, want=%v", c.cmd, got, c.want)
		}
	}
}

func TestEnvAllowParsed(t *testing.T) {
	path := write(t, `{"servers": [{"name": "s", "command": ["x"], "envAllow": ["GITHUB_TOKEN"]}]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Servers[0].EnvAllow; len(got) != 1 || got[0] != "GITHUB_TOKEN" {
		t.Fatalf("envAllow: %v", got)
	}
}
