package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Запись с сессионной cookie без Origin — подделка: браузер шлёт Origin всегда.
// API по заголовку (прокси-аутентификация) и вход cookie не имеют и проходят.
func TestCSRFCookieRequiresOrigin(t *testing.T) {
	e := newEnv(t, AuthOptions{Password: "pw"})

	w := e.do(t, http.MethodPost, "/api/login", map[string]any{"user": "admin", "password": "pw"})
	status(t, w, http.StatusOK)
	cookie := w.Result().Cookies()[0]

	body := `{"mcpServers":{}}`

	w = e.doWithCookieOrigin(t, http.MethodPost, "/api/import", body, cookie, "")
	status(t, w, http.StatusForbidden)
	if !strings.Contains(w.Body.String(), "нужен заголовок Origin") {
		t.Fatalf("текст отказа: %s", w.Body.String())
	}

	w = e.doWithCookieOrigin(t, http.MethodPost, "/api/import", body, cookie, "http://evil.example")
	status(t, w, http.StatusForbidden)

	w = e.doWithCookieOrigin(t, http.MethodPost, "/api/import", body, cookie, "http://example.com")
	status(t, w, http.StatusOK)
}

// API-клиент с заголовками прокси не шлёт Origin — и не должен: CSRF-проверка
// висит только на cookie.
func TestCSRFProxyAuthWithoutOrigin(t *testing.T) {
	e := newEnv(t, AuthOptions{ProxySecret: "s3cr3t", UserHeader: "Remote-User"})

	r := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"mcpServers":{}}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Remote-User", "alice")
	r.Header.Set("x-mcp-proxy-secret", "s3cr3t")
	w := httptest.NewRecorder()
	e.m.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200: %s", w.Code, w.Body.String())
	}
}
