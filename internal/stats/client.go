package stats

import "context"

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
