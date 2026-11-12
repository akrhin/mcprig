package manager

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// checkCSRF за обратным прокси: Origin остаётся публичным, Host приходит
// внутренним, публичное имя — в X-Forwarded-Host. MCPRIG_ALLOWED_HOSTS —
// список имён самой панели, источником он не является.
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
		{"origin из allowedHosts не источник", "http://mcprig.lan:9088", "127.0.0.1:9088", "", http.StatusForbidden, "origin не совпадает с хостом"},
		{"тот же хост, другой порт", "http://192.168.1.92:3000", "192.168.1.92:9088", "", http.StatusForbidden, "origin не совпадает с хостом"},
		{"другой хост", "http://evil.example", "192.168.1.92:9088", "", http.StatusForbidden, "origin не совпадает с хостом"},
		{"за прокси", "https://mcprig.example", "127.0.0.1:9088", "mcprig.example", http.StatusOK, ""},
		{"за прокси, чужой порт", "https://mcprig.example:3000", "127.0.0.1:9088", "mcprig.example", http.StatusForbidden, "origin не совпадает с хостом"},
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
		{"http запрос, origin https", "https://example.com", "", false, http.StatusForbidden, "origin не совпадает с хостом"},
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

// TLS-прокси без X-Forwarded-Proto: доверенный прокси (MCPRIG_PROXY_SECRET)
// даёт право считать https-Origin своим — иначе вход за nginx с одним лишь
// proxy_set_header Host давал 403 ещё до пароля. http-Origin при том же
// доверии остаётся чужим.
func TestCSRFTrustedProxyNoXFP(t *testing.T) {
	secret := strings.Repeat("s", 32)
	e := newEnv(t, AuthOptions{ProxySecret: secret})

	post := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"mcpServers":{}}`))
		req.Host = "example.com"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("Remote-User", "alice")
		req.Header.Set("X-MCP-Proxy-Secret", secret)
		w := httptest.NewRecorder()
		e.m.ServeHTTP(w, req)
		return w
	}
	if w := post("https://example.com"); w.Code != http.StatusOK {
		t.Fatalf("доверенный прокси без XFP: https-Origin отвергнут: %d (%s)", w.Code, w.Body.String())
	}
	if w := post("http://example.com"); w.Code != http.StatusForbidden {
		t.Fatalf("http-Origin на доверенном прокси прошёл: %d (%s)", w.Code, w.Body.String())
	}
	if w := post("https://example.com:3000"); w.Code != http.StatusForbidden {
		t.Fatalf("чужой порт прошёл: %d (%s)", w.Code, w.Body.String())
	}
}

// Вход не должен зависеть от Origin: пароль проверяется отдельно, а старая
// cookie из браузера превращала POST /api/login в 403 «нужен заголовок Origin».
func TestCSRFLoginWithoutOrigin(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"user":"admin","password":"x"}`))
	req.Host = "example.com"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test"})
	if err := e.m.checkCSRF(req); err != nil {
		t.Fatalf("вход со старой cookie без Origin: %v", err)
	}
}
