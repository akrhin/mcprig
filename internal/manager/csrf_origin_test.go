package manager

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// checkCSRF за обратным прокси: Origin остаётся публичным, Host приходит
// внутренним, публичное имя — в X-Forwarded-Host или в MCPRIG_ALLOWED_HOSTS.
// Код 200 здесь значит «дошло до обработчика»; 403 проверяется по тексту отказа,
// чтобы не спутать CSRF с проверкой Host.
func TestCSRFOriginBehindProxy(t *testing.T) {
	hosts := []string{"192.168.1.92", "mcprig.lan", "mcprig.example", "127.0.0.1"}
	e := newEnv(t, AuthOptions{Off: true, AllowedHosts: hosts})

	cases := []struct {
		name    string
		origin  string
		host    string
		forward string
		code    int
		want    string
	}{
		{"origin = host", "http://192.168.1.92:9088", "192.168.1.92:9088", "", http.StatusOK, ""},
		{"origin из allowedHosts", "http://mcprig.lan:9088", "127.0.0.1:9088", "", http.StatusOK, ""},
		{"за прокси", "https://mcprig.example", "127.0.0.1:9088", "mcprig.example", http.StatusOK, ""},
		{"чужой origin", "http://evil.example", "192.168.1.92:9088", "", http.StatusForbidden, "origin не совпадает с хостом"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"mcpServers":{}}`))
			req.Host = c.host
			req.Header.Set("Origin", c.origin)
			req.Header.Set("Content-Type", "application/json")
			if c.forward != "" {
				req.Header.Set("X-Forwarded-Host", c.forward)
			}
			w := httptest.NewRecorder()
			e.m.ServeHTTP(w, req)
			if w.Code != c.code {
				t.Fatalf("%s: код %d (%s)", c.name, w.Code, w.Body.String())
			}
			if c.want != "" && !strings.Contains(w.Body.String(), c.want) {
				t.Fatalf("%s: тело отказа %q не содержит %q", c.name, w.Body.String(), c.want)
			}
		})
	}
}

// Схема Origin сверяется с фактической: X-Forwarded-Proto и TLS. Записи с
// сессионной cookie без Origin (и с Origin: null) — отказ.
func TestCSRFScheme(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	cookie := &http.Cookie{Name: sessionCookie, Value: "test"}

	cases := []struct {
		name   string
		origin string
		proto  string
		tls    bool
		code   int
		want   string
	}{
		{"прокси https, origin https", "https://example.com", "https", false, http.StatusOK, ""},
		{"tls, origin https", "https://example.com", "", true, http.StatusOK, ""},
		{"прокси https, origin http", "http://example.com", "https", false, http.StatusForbidden, "origin не совпадает с хостом"},
		{"origin null с cookie", "null", "", false, http.StatusForbidden, "нужен заголовок Origin"},
		{"без origin с cookie", "", "", false, http.StatusForbidden, "нужен заголовок Origin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"mcpServers":{}}`))
			req.Host = "example.com"
			req.Header.Set("Content-Type", "application/json")
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			if c.proto != "" {
				req.Header.Set("X-Forwarded-Proto", c.proto)
			}
			if c.tls {
				req.TLS = &tls.ConnectionState{}
			}
			req.AddCookie(cookie)
			w := httptest.NewRecorder()
			e.m.ServeHTTP(w, req)
			if w.Code != c.code {
				t.Fatalf("%s: код %d (%s)", c.name, w.Code, w.Body.String())
			}
			if c.want != "" && !strings.Contains(w.Body.String(), c.want) {
				t.Fatalf("%s: тело отказа %q не содержит %q", c.name, w.Body.String(), c.want)
			}
		})
	}
}
