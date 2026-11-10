package stats

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

type clientKey struct{}

// WithClient labels the call in flight with the harness behind it: a key id
// plus User-Agent product token for HTTP callers. The request's _meta client
// info wins over this label when it is present. The recorder falls back to
// "unknown".
func WithClient(ctx context.Context, client string) context.Context {
	if client == "" {
		return ctx
	}
	return context.WithValue(ctx, clientKey{}, client)
}

// ClientFrom reports the label set by the transport, or fallback.
func ClientFrom(ctx context.Context, fallback string) string {
	if v, ok := ctx.Value(clientKey{}).(string); ok && v != "" {
		return v
	}
	if fallback == "" {
		return "unknown"
	}
	return fallback
}

// Fingerprint — 6 hex от sha256 токена. По нему ключ узнаётся в метке клиента,
// а сам токен в статистику и в конфиг набора не попадает.
func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:6]
}

// Label builds the HTTP client label: "key:<id>" plus the User-Agent product
// token, so two harnesses sharing one key stay distinguishable.
func Label(keyID, userAgent string) string {
	switch {
	case keyID != "" && userAgent != "":
		return "key:" + keyID + "/" + userAgent
	case keyID != "":
		return "key:" + keyID
	default:
		return userAgent
	}
}
