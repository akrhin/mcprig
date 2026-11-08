package main

import (
	"slices"
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
