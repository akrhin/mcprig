package mcpserver

import (
	"fmt"
	"sync"
	"testing"

	"github.com/akrhin/mcprig/internal/config"
)

func allowRule(pattern string) config.Rule {
	return config.Rule{Pattern: pattern, Action: config.PermissionAllow}
}

func denyRule(pattern string) config.Rule {
	return config.Rule{Pattern: pattern, Action: config.PermissionDeny}
}

// The gate follows lazy-mcp's order: server rules, global rules, server default,
// global default, implicit allow.
func TestDenyReason(t *testing.T) {
	globalDeny := &config.Permissions{Default: config.PermissionDeny}
	empty := &config.Permissions{}

	tests := []struct {
		name       string
		global     *config.Permissions
		server     *config.Permissions
		serverName string
		command    string
		wantDeny   bool
		wantMsg    string
	}{
		{
			name:   "серверное allow перекрывает глобальный default deny",
			global: globalDeny,
			server: &config.Permissions{Rules: []config.Rule{
				{Pattern: "read_*", Action: config.PermissionAllow},
			}},
			serverName: "alpha", command: "read_all",
		},
		{
			name:   "прочее под глобальным default deny остаётся запрещённым",
			global: globalDeny,
			server: &config.Permissions{Rules: []config.Rule{
				{Pattern: "read_*", Action: config.PermissionAllow},
			}},
			serverName: "alpha", command: "write_all", wantDeny: true,
		},
		{
			name:       "серверный default deny побеждает пустой глобальный default",
			global:     empty,
			server:     &config.Permissions{Default: config.PermissionDeny},
			serverName: "alpha", command: "anything", wantDeny: true,
		},
		{
			name: "последнее совпавшее правило уровня решает",
			server: &config.Permissions{Rules: []config.Rule{
				allowRule("*"),
				denyRule("danger_*"),
			}},
			serverName: "alpha", command: "danger_rm", wantDeny: true,
		},
		{
			name: "предыдущее правило из пары работает",
			server: &config.Permissions{Rules: []config.Rule{
				allowRule("*"),
				denyRule("danger_*"),
			}},
			serverName: "alpha", command: "safe_read",
		},
		{
			name: "глобальное правило с сервером не задевает другой сервер",
			global: &config.Permissions{Rules: []config.Rule{
				{Server: "alpha", Pattern: "write_*", Action: config.PermissionDeny, Message: "read only"},
			}},
			serverName: "beta", command: "write_x",
		},
		{
			name: "глобальное правило с сервером срабатывает на своём",
			global: &config.Permissions{Rules: []config.Rule{
				{Server: "alpha", Pattern: "write_*", Action: config.PermissionDeny, Message: "read only"},
			}},
			serverName: "alpha", command: "write_x", wantDeny: true, wantMsg: "read only",
		},
		{
			name:       "глобальное правило без сервера бьёт по всем",
			global:     &config.Permissions{Rules: []config.Rule{denyRule("write_*")}},
			serverName: "beta", command: "write_x", wantDeny: true,
		},
		{
			name: "* — любая строка",
			server: &config.Permissions{Rules: []config.Rule{
				denyRule("danger_*"),
			}},
			serverName: "alpha", command: "danger_", wantDeny: true,
		},
		{
			name: "? — ровно один символ",
			server: &config.Permissions{Rules: []config.Rule{
				denyRule("get_?"),
			}},
			serverName: "alpha", command: "get_ab",
		},
		{
			name: "? — один символ совпадает",
			server: &config.Permissions{Rules: []config.Rule{
				denyRule("get_?"),
			}},
			serverName: "alpha", command: "get_a", wantDeny: true,
		},
		{
			name: "** работает как *",
			server: &config.Permissions{Rules: []config.Rule{
				denyRule("get**all"),
			}},
			serverName: "alpha", command: "get_123all", wantDeny: true,
		},
		{
			name: "регистр не важен",
			server: &config.Permissions{Rules: []config.Rule{
				denyRule("READ_*"),
			}},
			serverName: "ALPHA", command: "read_x", wantDeny: true,
		},
		{
			name:       "без политики всё разрешено",
			serverName: "alpha", command: "anything",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, denied := denyReason(tc.global, tc.server, tc.serverName, tc.command)
			if denied != tc.wantDeny {
				t.Fatalf("denyReason = %v, want %v (msg %q)", denied, tc.wantDeny, msg)
			}
			if msg != tc.wantMsg {
				t.Fatalf("msg = %q, want %q", msg, tc.wantMsg)
			}
		})
	}
}

// Two patterns in one process must keep their own compiled form: a cache keyed
// by the wrong field would answer one rule with the other rule's regexp.
func TestGlobMatchKeepsPatternsApart(t *testing.T) {
	if !globMatch("read_*", "read_x") || globMatch("read_*", "write_x") {
		t.Fatal("read_* ответил не по своему правилу")
	}
	if !globMatch("write_*", "write_x") || globMatch("write_*", "read_x") {
		t.Fatal("write_* ответил не по своему правилу")
	}
	if globMatch("get_?", "get_ab") {
		t.Fatal("get_? не должен совпадать с двумя символами")
	}
}

// The cache is filled under the race detector by the usual parallel callers.
func TestGlobMatchConcurrent(t *testing.T) {
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pattern := fmt.Sprintf("cmd_%d_*", i)
			if !globMatch(pattern, fmt.Sprintf("cmd_%d_x", i)) {
				errs <- pattern + ": не совпал со своим именем"
				return
			}
			if globMatch(pattern, fmt.Sprintf("cmd_%d_x", (i+1)%workers)) {
				errs <- pattern + ": совпал с чужим именем"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}
