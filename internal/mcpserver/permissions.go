// Policy answer for invoke_command, compatible with lazy-mcp's experimental
// `permissions` block. The gate itself lives in internal/config so the panel's
// browse path can apply the same rules.
package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// denyReason applies lazy-mcp's resolution order and reports whether the call
// is denied, together with the rule's message.
func denyReason(global, server *config.Permissions, serverName, command string) (string, bool) {
	return config.DenyReason(global, server, serverName, command)
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
