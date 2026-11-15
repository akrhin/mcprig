package registry

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// MaxActiveKeys — сколько действующих ключей держит один набор. Потолок
// защищает от забытого «выдать ещё один»: лишний ключ придётся отозвать.
const MaxActiveKeys = 8

// activeToken is the token of the first active key (the set's primary): /stats
// and the health probe ask for it, so it must stay stable while other keys are
// issued or revoked.
func activeToken(keys []Key) string {
	for _, key := range keys {
		if key.RevokedAt == "" {
			return key.Token
		}
	}
	return ""
}

// activeTokens is every token a client may present, in registration order.
func activeTokens(keys []Key) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if key.RevokedAt == "" && key.Token != "" {
			out = append(out, key.Token)
		}
	}
	return out
}

// ActiveTokens is activeTokens for callers outside the package.
func ActiveTokens(keys []Key) []string { return activeTokens(keys) }

func activeKeyCount(keys []Key) int {
	n := 0
	for _, key := range keys {
		if key.RevokedAt == "" {
			n++
		}
	}
	return n
}

// keyPrefix is the first 8 characters of the primary token.
func keyPrefix(keys []Key) string {
	token := activeToken(keys)
	if len(token) > 8 {
		return token[:8]
	}
	return token
}

// newKey makes one active key with a fresh 48-hex token and 12-hex id.
func newKey(now string) Key {
	return Key{ID: randomHex(6), Token: randomHex(24), CreatedAt: now}
}

// freshKeyID is a new key id that no key in keys — active or revoked — uses
// yet: revoked keys stay in the list, so their ids are taken too.
func freshKeyID(keys []Key) string {
	taken := make(map[string]bool, len(keys))
	for _, key := range keys {
		taken[key.ID] = true
	}
	for {
		if id := randomHex(6); !taken[id] {
			return id
		}
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	// crypto/rand never fails on a supported system; a zero token would be the
	// least of the problems if it did.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// safeKeyID отвергает id ключа, с которым можно выйти из каталога набора.
func safeKeyID(id string) bool {
	return !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

// normalizeKeys drops malformed and duplicate keys, fills a missing createdAt
// and carries the legacy "note" over to "label". Every key without revokedAt
// stays active: a set may serve several keys at once, and revocation is only
// written by RevokeKey. A set with no keys at all inherits the legacy single
// `token`, if any.
func normalizeKeys(keys []Key, legacyToken, now string) []Key {
	out := make([]Key, 0, len(keys)+1)
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		key.ID = strings.TrimSpace(key.ID)
		key.Token = strings.TrimSpace(key.Token)
		if key.ID == "" || key.Token == "" || seen[key.ID] || !safeKeyID(key.ID) {
			continue
		}
		seen[key.ID] = true
		if key.CreatedAt == "" {
			key.CreatedAt = now
		}
		if key.Label == "" {
			key.Label = key.Note
		}
		out = append(out, key)
	}
	if len(out) == 0 {
		if legacy := strings.TrimSpace(legacyToken); legacy != "" {
			out = append(out, Key{ID: LegacyKeyID, Token: legacy, CreatedAt: now})
		}
	}
	return out
}
