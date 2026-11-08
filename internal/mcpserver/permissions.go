// Permission gate for invoke_command, compatible with lazy-mcp's experimental
// `permissions` block. Without a block every call is allowed.
package mcpserver

import (
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcp-manager/internal/config"
)

// denyReason applies lazy-mcp's resolution order and reports whether the call
// is denied, together with the rule's message.
//
// Order (first non-empty wins, last matching rule within a level wins):
// server rules, global rules, server default, global default, implicit allow.
func denyReason(global, server *config.Permissions, serverName, command string) (string, bool) {
	if server != nil {
		if r := lastMatch(server.Rules, func(r config.Rule) bool { return globMatch(r.Pattern, command) }); r != nil {
			return r.Message, r.Action == config.PermissionDeny
		}
	}
	if global != nil {
		if r := lastMatch(global.Rules, func(r config.Rule) bool {
			serverPattern := r.Server
			if serverPattern == "" {
				serverPattern = "*"
			}
			return globMatch(serverPattern, serverName) && globMatch(r.Pattern, command)
		}); r != nil {
			return r.Message, r.Action == config.PermissionDeny
		}
	}
	if server != nil && server.Default == config.PermissionDeny {
		return "", true
	}
	if global != nil && global.Default == config.PermissionDeny {
		return "", true
	}
	return "", false
}

func lastMatch(rules []config.Rule, match func(config.Rule) bool) *config.Rule {
	var found *config.Rule
	for i := range rules {
		if match(rules[i]) {
			found = &rules[i]
		}
	}
	return found
}

// globMatch is case-insensitive; `*` is any run of characters, `?` exactly one.
// Runs of `*` collapse first, which defuses the backtracking blow-up.
func globMatch(pattern, value string) bool {
	for strings.Contains(pattern, "**") {
		pattern = strings.ReplaceAll(pattern, "**", "*")
	}
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

// blockedResult is the lazy-mcp policy shape host hooks look for.
func blockedResult(serverName, command, message string) *mcp.CallToolResult {
	if message == "" {
		message = "tool '" + serverName + ":" + command + "' is denied"
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "🚫 Blocked by mcp-manager policy: " + message}},
		Meta:    mcp.Meta{"lazyMcpPolicy": "blocked"},
	}
}
