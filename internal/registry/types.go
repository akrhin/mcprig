// Package registry owns the manager's persistent state: downstream servers,
// sets (one lazy-mcp instance each), their access keys and the port allocator.
// The format is registry.json from the TS reference, with the defects fixed:
// ports stay inside 9100–9199, tokens never leave through PublicSet, env and
// headers accept strings only, and writes are atomic.
package registry

import "github.com/akrhin/mcprig/internal/config"

const (
	// RegistryFile is the registry file name inside the data directory.
	RegistryFile = "registry.json"
	// FirstPort and LastPort bound the published set ports.
	FirstPort = 9100
	LastPort  = 9199
	// LegacyKeyID is the id of the key back-filled from the old `token` field.
	LegacyKeyID = "legacy"
)

// Server is one registry entry: exactly the shape lazy-mcp accepts in
// servers.json.
type Server = *config.Server

// Key is one access key of a set. lazy-mcp takes a single auth token, so at
// most one key is active (RevokedAt empty); the rest is revocation history.
type Key struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	CreatedAt string `json:"createdAt"`
	RevokedAt string `json:"revokedAt,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Set is a named group of servers deployed as its own lazy-mcp instance.
// Token mirrors the active key and is never sent to a client as is.
type Set struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Servers     []string `json:"servers"`
	Port        int      `json:"port"`
	Keys        []Key    `json:"keys"`
	Token       string   `json:"token"`
	Autostart   bool     `json:"autostart,omitempty"`
	Open        bool     `json:"open,omitempty"`
}

// Data is registry.json.
type Data struct {
	Version  int      `json:"version"`
	Servers  []Server `json:"servers"`
	Sets     []*Set   `json:"sets"`
	NextPort int      `json:"nextPort"`
}

// CanServe reports whether a set may be started: it has an active key or is
// explicitly open. A set with no key and not open must never run.
func CanServe(set *Set) bool {
	return set != nil && (set.Open || activeToken(set.Keys) != "")
}

// PublicKey is a key as a client may see it: no token.
type PublicKey struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	RevokedAt string `json:"revokedAt,omitempty"`
	Note      string `json:"note,omitempty"`
}

// PublicSetEntry is the client-facing view of a set: no raw token, only the
// active key's 8-character prefix.
type PublicSetEntry struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Servers     []string    `json:"servers"`
	Port        int         `json:"port"`
	Keys        []PublicKey `json:"keys"`
	KeyPrefix   string      `json:"keyPrefix"`
	Autostart   bool        `json:"autostart,omitempty"`
	Open        bool        `json:"open,omitempty"`
}
