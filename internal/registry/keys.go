package registry

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// activeToken is the token of the single active key; empty when none.
func activeToken(keys []Key) string {
	for _, key := range keys {
		if key.RevokedAt == "" {
			return key.Token
		}
	}
	return ""
}

// keyPrefix is the first 8 characters of the active token.
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

func randomHex(n int) string {
	b := make([]byte, n)
	// crypto/rand never fails on a supported system; a zero token would be the
	// least of the problems if it did.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// normalizeKeys repairs the "exactly one active key" invariant: invalid keys
// are dropped, the first active key wins, the rest are revoked at now. A set
// with no keys at all inherits the legacy single `token`, if any.
func normalizeKeys(keys []Key, legacyToken, now string) []Key {
	out := make([]Key, 0, len(keys)+1)
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		key.ID = strings.TrimSpace(key.ID)
		key.Token = strings.TrimSpace(key.Token)
		if key.ID == "" || key.Token == "" || seen[key.ID] {
			continue
		}
		seen[key.ID] = true
		if key.CreatedAt == "" {
			key.CreatedAt = now
		}
		out = append(out, key)
	}
	if len(out) == 0 {
		if legacy := strings.TrimSpace(legacyToken); legacy != "" {
			out = append(out, Key{ID: LegacyKeyID, Token: legacy, CreatedAt: now})
		}
		return out
	}
	active := false
	for i := range out {
		if out[i].RevokedAt != "" {
			continue
		}
		if active {
			out[i].RevokedAt = now
		} else {
			active = true
		}
	}
	return out
}
