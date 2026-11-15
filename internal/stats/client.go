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

type clientFPKey struct{}

// WithClientKey carries the access key's fingerprint down the request. It is
// separate from the display label on purpose: the label can be a note the owner
// gave the key or a clientInfo the caller sends, and neither identifies a key.
// The rate limiter buckets by this value, so a caller cannot mint quotas by
// changing what it calls itself.
func WithClientKey(ctx context.Context, fp string) context.Context {
	if fp == "" {
		return ctx
	}
	return context.WithValue(ctx, clientFPKey{}, fp)
}

// ClientKey reports the fingerprint set by the transport; empty for a request
// that carried no key (an open set, or stdio).
func ClientKey(ctx context.Context) string {
	v, _ := ctx.Value(clientFPKey{}).(string)
	return v
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
