package config

import (
	"strings"
	"testing"
)

// TestExposeParsedStrictly: expose is a whitelist of surfaces; a value nobody
// knows leaves the server unusable instead of silently exposing nothing.
func TestExposeParsedStrictly(t *testing.T) {
	cfg, err := Load(write(t, `{
	  "servers": [
	    {"name": "good", "command": ["downstream"], "expose": ["resources", "prompts"]},
	    {"name": "typo", "command": ["downstream"], "expose": ["ressources"]},
	    {"name": "wrong-type", "command": ["downstream"], "expose": "resources"},
	    {"name": "none", "command": ["downstream"]}
	  ]
	}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	byName := map[string]*Server{}
	for _, s := range cfg.Servers {
		byName[s.Name] = s
	}
	good := byName["good"]
	if ok := good.validate(); !ok || len(good.Expose) != 2 || good.Expose[0] != ExposeResources || good.Expose[1] != ExposePrompts {
		t.Errorf("good: %v usable=%v (%s)", good.Expose, ok, good.ConfigError)
	}
	typo := byName["typo"]
	if ok := typo.validate(); ok || !strings.Contains(typo.ConfigError, `expose: неизвестный вид "ressources"`) {
		t.Errorf("typo: %q, usable=%v", typo.ConfigError, ok)
	}
	wrong := byName["wrong-type"]
	if ok := wrong.validate(); ok || !strings.Contains(wrong.ConfigError, "expose: ожидался массив строк") {
		t.Errorf("wrong-type: %q, usable=%v", wrong.ConfigError, ok)
	}
	none := byName["none"]
	if ok := none.validate(); !ok || len(none.Expose) != 0 {
		t.Errorf("none: %v usable=%v (%s)", none.Expose, ok, none.ConfigError)
	}
	// Check is the outside-of-file entry point (registry, probe): it must see
	// the same defect, not just a hand-built Load.
	built := &Server{Name: "typo", Transport: TransportStdio, Command: []string{"downstream"}, Expose: []string{"ressources"}}
	if msg := built.Check(); !strings.Contains(msg, "expose: неизвестный вид") {
		t.Errorf("Check: %q", msg)
	}
}
