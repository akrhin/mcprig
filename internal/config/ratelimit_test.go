package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// A broken rateLimit is dropped so a typo never bricks the set, but not
// silently: the owner must see that the limit they wrote is not in effect.
func TestConfigLoadDropsBrokenRateLimit(t *testing.T) {
	var log bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	defer slog.SetDefault(prev)

	path := write(t, `{"servers":[{"name":"a","command":["x"]}],"rateLimit":{"perMunute":10}}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RateLimit != nil {
		t.Fatalf("битый блок принят: %+v", cfg.RateLimit)
	}
	warn := log.String()
	if !strings.Contains(warn, "rateLimit") || !strings.Contains(warn, "perMunute") {
		t.Fatalf("в логе нет предупреждения с причиной: %q", warn)
	}

	// Годный блок по-прежнему доезжает и предупреждения не даёт.
	log.Reset()
	path = write(t, `{"servers":[{"name":"a","command":["x"]}],"rateLimit":{"perMinute":120,"burst":20}}`)
	if cfg, err = Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RateLimit == nil || cfg.RateLimit.PerMinute != 120 || cfg.RateLimit.Burst != 20 {
		t.Fatalf("лимит не прочитан: %+v", cfg.RateLimit)
	}
	if log.Len() != 0 {
		t.Fatalf("годный блок дал предупреждение: %q", log.String())
	}
}
