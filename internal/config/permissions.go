// Permission gate for tool calls, compatible with lazy-mcp's experimental
// `permissions` block. Without a block every call is allowed.
package config

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

// ParsePermissions reads an allow/deny block. The second result describes a
// defect and is empty when the block is usable; an absent or null block gives
// (nil, ""). The loader soft-fails a defect, the registry API rejects it — both
// go through here so the rules cannot drift apart.
func ParsePermissions(raw json.RawMessage) (*Permissions, string) {
	if trimmed := strings.TrimSpace(string(raw)); trimmed == "" || trimmed == "null" {
		return nil, ""
	}
	var p Permissions
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, "permissions: " + err.Error()
	}
	if msg := p.validate("permissions", false); msg != "" {
		return nil, msg
	}
	return &p, ""
}

// DenyReason applies lazy-mcp's resolution order and reports whether the call
// is denied, together with the rule's message.
//
// Order (first non-empty wins, last matching rule within a level wins):
// server rules, global rules, server default, global default, implicit allow.
func DenyReason(global, server *Permissions, serverName, command string) (string, bool) {
	if server != nil {
		if r := lastMatch(server.Rules, func(r Rule) bool { return globMatch(r.Pattern, command) }); r != nil {
			return r.Message, r.Action == PermissionDeny
		}
	}
	if global != nil {
		if r := lastMatch(global.Rules, func(r Rule) bool {
			serverPattern := r.Server
			if serverPattern == "" {
				serverPattern = "*"
			}
			return globMatch(serverPattern, serverName) && globMatch(r.Pattern, command)
		}); r != nil {
			return r.Message, r.Action == PermissionDeny
		}
	}
	if server != nil && server.Default == PermissionDeny {
		return "", true
	}
	if global != nil && global.Default == PermissionDeny {
		return "", true
	}
	return "", false
}

func lastMatch(rules []Rule, match func(Rule) bool) *Rule {
	var found *Rule
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
	re := compiledGlob(pattern)
	return re != nil && re.MatchString(value)
}

// globCache holds one compiled pattern per distinct rule: the gate used to
// compile every pattern on every call. Keyed by the normalized pattern, which
// is the same string for the same rule; the config set is bounded, so the map
// does not grow at runtime. (Precompiling in the loader would need a hook per
// config source; this cache compiles once per pattern instead.)
var globCache sync.Map // normalized pattern -> *regexp.Regexp (nil when invalid)

func compiledGlob(pattern string) *regexp.Regexp {
	for strings.Contains(pattern, "**") {
		pattern = strings.ReplaceAll(pattern, "**", "*")
	}
	if cached, ok := globCache.Load(pattern); ok {
		re, _ := cached.(*regexp.Regexp)
		return re
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
		re = nil
	}
	globCache.Store(pattern, re)
	return re
}
