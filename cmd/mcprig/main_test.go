package main

import (
	"errors"
	"flag"
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

// -h/-help — не ошибка: usage напечатан, код возврата 0. Настоящая ошибка
// разбора флагов по-прежнему код 2.
func TestHelpIsNotAnError(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"serve", "-h"}} {
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
