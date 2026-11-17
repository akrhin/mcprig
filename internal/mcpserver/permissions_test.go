package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
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
// global default, implicit allow. The glob behaviour and the cache are covered
// next to the implementation in internal/config/permissions_test.go.
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

// permissions.deny скрывает команду из каталога и схем: ключ не должен искать
// работу через имя, которое invoke_command отклонит. Сам invoke остаётся
// отказом политики, а не «неизвестным инструментом».
func TestPermissionsFilterCatalog(t *testing.T) {
	perms := &config.Permissions{Rules: []config.Rule{
		{Server: "alpha", Pattern: "danger-*", Action: config.PermissionDeny, Message: "запрещено набором"},
	}}
	m := profileManager(t, nil, perms)
	ctx := context.Background()

	res, err := m.listCommands(ctx, toolReq("list_commands", `{"server":"alpha"}`))
	if err != nil {
		t.Fatalf("listCommands: %v", err)
	}
	names := listNames(t, res)
	if hasName(names, "danger-zone") {
		t.Fatalf("deny отдал запрещённый инструмент: %v", names)
	}
	if !hasName(names, "echo") {
		t.Fatalf("deny унёс разрешённые: %v", names)
	}

	// Мультисерверный запрос фильтруется так же.
	res, err = m.listCommands(ctx, toolReq("list_commands", `{"servers":["alpha","beta"]}`))
	if err != nil {
		t.Fatalf("listCommands: %v", err)
	}
	var byServer map[string][]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &byServer); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	for _, c := range byServer["alpha"] {
		if c.Name == "danger-zone" {
			t.Fatalf("alpha отдал запрещённый инструмент")
		}
	}
	// Правило привязано к alpha: соседа оно не задевает.
	if len(byServer["beta"]) == 0 {
		t.Fatalf("правило alpha опустошило beta")
	}

	// describe_commands: схемы запрещённого нет, имя — в missing.
	res, err = m.describeCommands(ctx, toolReq("describe_commands", `{"server":"alpha","command_names":["danger-zone","echo"]}`))
	if err != nil || res.IsError {
		t.Fatalf("describeCommands: %v %s", err, resultText(t, res))
	}
	var out describeOut
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("разбор схем: %v", err)
	}
	if len(out.Commands) != 1 || out.Commands[0].Name != "echo" {
		t.Fatalf("схемы запрещённого: %+v", out.Commands)
	}
	if len(out.Missing) != 1 || out.Missing[0] != "danger-zone" {
		t.Fatalf("missing: %v", out.Missing)
	}

	// Вызов — отказ правила, а не подмена на «неизвестный инструмент».
	res, err = m.invokeCommand(ctx, toolReq("invoke_command", `{"server":"alpha","command_name":"danger-zone"}`))
	if err != nil {
		t.Fatalf("invokeCommand: %v", err)
	}
	text := resultText(t, res)
	if !res.IsError || !strings.Contains(text, "запрещено набором") {
		t.Fatalf("ждали отказ политики, получили %q", text)
	}
	if strings.Contains(text, "неизвестн") {
		t.Fatalf("отказ выдан за неизвестный инструмент: %q", text)
	}
}
