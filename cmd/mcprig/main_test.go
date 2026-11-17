package main

import (
	"bytes"
	"errors"
	"flag"
	"slices"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		mode string
		rest []string
		want bool // err != nil
	}{
		{"пусто — serve", nil, "serve", nil, false},
		{"serve", []string{"serve"}, "serve", nil, false},
		{"флаги без подкоманды", []string{"-config", "x"}, "serve", []string{"-config", "x"}, false},
		{"serve с флагами", []string{"serve", "--addr", ":9100"}, "serve", []string{"--addr", ":9100"}, false},
		{"stdio с флагами", []string{"stdio", "--config", "x"}, "stdio", []string{"--config", "x"}, false},
		{"panel", []string{"panel"}, "panel", nil, false},
		{"init с флагами", []string{"init", "--client", "codex"}, "init", []string{"--client", "codex"}, false},
		{"опечатка подкоманды", []string{"serv", "--config", "x"}, "", nil, true},
		{"лишний позиционный аргумент", []string{"serve", "foo"}, "", nil, true},
		{"panel с аргументом", []string{"panel", "foo"}, "", nil, true},
	}
	for _, c := range cases {
		mode, rest, err := parseArgs(c.in)
		if (err != nil) != c.want {
			t.Errorf("%s: err = %v, want error %v", c.name, err, c.want)
			continue
		}
		if c.want {
			continue
		}
		if mode != c.mode {
			t.Errorf("%s: mode = %q, want %q", c.name, mode, c.mode)
		}
		if !slices.Equal(rest, c.rest) {
			t.Errorf("%s: rest = %q, want %q", c.name, rest, c.rest)
		}
	}
}

// panel -h печатает переменные окружения: флагов у режима нет.
func TestPanelHelpListsEnv(t *testing.T) {
	fs := newPanelFlags()
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	if err := fs.Parse([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("panel -h: err = %v, ожидался flag.ErrHelp", err)
	}
	for _, name := range []string{
		"MCPRIG_DATA_DIR", "XDG_STATE_HOME", "MCPRIG_PORT", "MCPRIG_PASSWORD", "MCPRIG_AUTH",
		"MCPRIG_TOOLOUT_TTL",
		"BAO_ADDR", "OPENBAO_ADDR", "BAO_TOKEN", "OPENBAO_TOKEN", "BAO_MOUNT", "BAO_PREFIX",
	} {
		if !strings.Contains(buf.String(), name) {
			t.Fatalf("help не назвал %s:\n%s", name, buf.String())
		}
	}
	// Число строк повторяют docs/{en,ru}/cli.md: разошлось — правится и помощь, и дока.
	if len(panelEnv) != 27 {
		t.Fatalf("panelEnv: %d переменных, в docs/*/cli.md записано 27", len(panelEnv))
	}
	seen := make(map[string]bool, len(panelEnv))
	for _, e := range panelEnv {
		if e[0] == "" || e[1] == "" {
			t.Fatalf("panelEnv: пустое имя или описание: %q", e)
		}
		if seen[e[0]] {
			t.Fatalf("panelEnv: %s указана дважды", e[0])
		}
		seen[e[0]] = true
	}
}

// -h/-help — не ошибка: usage напечатан, код возврата 0. Настоящая ошибка
// разбора флагов по-прежнему код 2.
func TestHelpIsNotAnError(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"serve", "-h"}, {"panel", "-h"}, {"panel", "--help"}} {
		_, _, err := parseArgs(args)
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("%v: err = %v, ожидался flag.ErrHelp", args, err)
		}
		if got := exitCode(err); got != 0 {
			t.Fatalf("%v: код %d, ожидался 0", args, got)
		}
	}
	if err := runInit([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("init -h: err = %v, ожидался flag.ErrHelp", err)
	}
	if got := exitCode(&usageError{"плохой флаг"}); got != 2 {
		t.Fatalf("usageError: код %d, ожидался 2", got)
	}
	if got := exitCode(errors.New("сбой")); got != 1 {
		t.Fatalf("прочая ошибка: код %d, ожидался 1", got)
	}
}

// Конфиг по умолчанию — ~/.config/mcprig/servers.json, не lazy-mcp.
func TestDefaultConfigPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-mcprig")
	if got, want := defaultConfigPath(), "/tmp/xdg-mcprig/mcprig/servers.json"; got != want {
		t.Fatalf("defaultConfigPath = %q, ожидалось %q", got, want)
	}
}
