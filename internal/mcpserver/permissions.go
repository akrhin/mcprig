// Policy answers for invoke_command: lazy-mcp's `permissions` block and, after
// it, the calling key's profile. The gate itself lives in internal/config so
// the panel's browse path can apply the same permission rules.
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
)

// denyReason applies lazy-mcp's resolution order and reports whether the call
// is denied, together with the rule's message.
func denyReason(global, server *config.Permissions, serverName, command string) (string, bool) {
	return config.DenyReason(global, server, serverName, command)
}

// callerProfile resolves the access key's profile from the request context. The
// key is found by its fingerprint, not by the display label: a label is a note
// the owner or the client chose. An empty name means no key (open set, stdio)
// or a key without a profile, which is unrestricted by profile.
//
// Отсутствие записи — это ключ без профиля (keyProfiles их не пишет), а не
// устаревшая строка: keepLiveTransport откатывает таблицу вместе с транспортом.
//
// cfg is the caller's snapshot: a reload between two reads could otherwise
// answer with one generation's token table and another's profiles.
func callerProfile(ctx context.Context, cfg *config.Config) (string, *config.Profile) {
	fp := stats.ClientKey(ctx)
	if fp == "" || cfg.Transport == nil {
		return "", nil
	}
	name := cfg.Transport.KeyProfiles[fp]
	if name == "" {
		return "", nil
	}
	return name, cfg.Profiles[name]
}

// gate applies permissions first, then the caller's profile: a profile narrows
// what permissions allowed and never widens it, so a denied call keeps the
// permissions message. A name without a matching block denies everything —
// deleting a profile must not silently widen a key that still refers to it.
//
// Одно решение читает один снимок конфига: глобальные правила и профиль идут
// из него, серверное правило — из снимка самой записи (perms). Два чтения
// m.config() давали окно, где решение смешивало два поколения.
func (m *Manager) gate(ctx context.Context, serverName, command string, perms *config.Permissions) (string, bool) {
	cfg := m.config()
	if message, denied := denyReason(cfg.Permissions, perms, serverName, command); denied {
		return message, true
	}
	name, profile := callerProfile(ctx, cfg)
	if name == "" {
		return "", false
	}
	if profile == nil || !profile.Allows(serverName, command) {
		return "профиль " + name + " не разрешает " + serverName + "/" + command, true
	}
	return "", false
}

// blockedResult is the lazy-mcp policy shape host hooks look for.
func blockedResult(serverName, command, message string) *mcp.CallToolResult {
	if message == "" {
		message = "tool '" + serverName + ":" + command + "' is denied"
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "🚫 Blocked by mcprig policy: " + message}},
		Meta:    mcp.Meta{"lazyMcpPolicy": "blocked"},
	}
}

// catalogPermissions drops the commands permissions deny, so the catalog never
// offers a name invoke_command refuses: a rule or a profile that hides a call
// must hide it everywhere the key can look.
func (m *Manager) catalogPermissions(cmds []commandInfo) []commandInfo {
	global := m.config().Permissions
	perServer := make(map[string]*config.Permissions, 2)
	out := make([]commandInfo, 0, len(cmds))
	for _, cmd := range cmds {
		srvPerms, seen := perServer[cmd.Server]
		if !seen {
			if e := m.find(cmd.Server); e != nil {
				srvPerms = e.server().Permissions
			}
			perServer[cmd.Server] = srvPerms
		}
		if _, denied := denyReason(global, srvPerms, cmd.Server, cmd.Name); denied {
			continue
		}
		out = append(out, cmd)
	}
	return out
}
