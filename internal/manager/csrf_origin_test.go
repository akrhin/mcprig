package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// checkCSRF за обратным прокси: Origin остаётся публичным, Host приходит
// внутренним, публичное имя — в X-Forwarded-Host или в MCPRIG_ALLOWED_HOSTS.
func TestCSRFOriginBehindProxy(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true, AllowedHosts: []string{"192.168.1.92", "mcprig.lan", "mcprig.example", "127.0.0.1"}})

	cases := []struct {
		name    string
		origin  string
		host    string
		forward string
		forbid  bool
	}{
		{"origin = host", "http://192.168.1.92:9088", "192.168.1.92:9088", "", false},
		{"origin из allowedHosts", "http://mcprig.lan:9088", "127.0.0.1:9088", "", false},
		{"за прокси", "https://mcprig.example", "127.0.0.1:9088", "mcprig.example", false},
		{"чужой origin", "http://evil.example", "192.168.1.92:9088", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/sets", strings.NewReader(`{}`))
			req.Host = c.host
			req.Header.Set("Origin", c.origin)
			req.Header.Set("Content-Type", "application/json")
			if c.forward != "" {
				req.Header.Set("X-Forwarded-Host", c.forward)
			}
			w := httptest.NewRecorder()
			e.m.ServeHTTP(w, req)
			// 403 — отказ CSRF или проверки Host; любой другой код значит, что
			// запрос дошёл до обработчика.
			if got := w.Code == http.StatusForbidden; got != c.forbid {
				t.Fatalf("%s: код %d (%s)", c.name, w.Code, w.Body.String())
			}
		})
	}
}
