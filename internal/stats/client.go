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

type clientAddrKey struct{}

// WithClientAddr carries the caller's address, port stripped, for an open set:
// with no key there is nothing else to bucket a rate limit by. Behind a proxy
// this is the proxy's address — a coarse divider, not a defence.
func WithClientAddr(ctx context.Context, addr string) context.Context {
	if addr == "" {
		return ctx
	}
	return context.WithValue(ctx, clientAddrKey{}, addr)
}

// ClientAddr reports the address set by the transport; empty for stdio.
func ClientAddr(ctx context.Context) string {
	v, _ := ctx.Value(clientAddrKey{}).(string)
	return v
}

// ServiceCallHeader marks a request the panel makes to its own set (health
// probe, catalog load): such a call is counted nowhere — no counters, no tail,
// no history.
const ServiceCallHeader = "X-MCPRig-Caller"

// ServiceCallerPanel is the value the manager sends on its own set requests.
const ServiceCallerPanel = "panel"

type serviceCallKey struct{}

// WithServiceCall marks the request as the panel's own.
//
// shortcut: держатель ключа набора может послать этот заголовок и скрыть свои
// вызовы из статистики; подписанный маркер, если это станет важно.
func WithServiceCall(ctx context.Context) context.Context {
	return context.WithValue(ctx, serviceCallKey{}, true)
}

// ServiceCall reports whether the transport marked this request as the panel's.
func ServiceCall(ctx context.Context) bool {
	v, _ := ctx.Value(serviceCallKey{}).(bool)
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
