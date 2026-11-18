package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SEC-08: X-Forwarded-* от пира вне MCPRIG_TRUSTED_PROXIES не действуют —
// поведение как без прокси, и запрос от этого не отвергается.
func TestForwardedHeadersTrustedPeerOnly(t *testing.T) {
	secret := strings.Repeat("s", 32)
	// Публичное имя панели живёт только в X-Forwarded-Host, Host внутренний.
	e := newEnv(t, AuthOptions{Off: true, ProxySecret: secret,
		TrustedProxies: "10.0.0.0/31", AllowedHosts: []string{"127.0.0.1", "mcprig.example"}})

	post := func(remote, origin, xfh string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"mcpServers":{}}`))
		req.Host = "127.0.0.1:9088"
		req.RemoteAddr = remote
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("Remote-User", "alice")
		req.Header.Set("X-MCP-Proxy-Secret", secret)
		req.Header.Set("X-Forwarded-Host", xfh)
		w := httptest.NewRecorder()
		e.m.ServeHTTP(w, req)
		return w.Code
	}

	// Чужой пир: https-Origin и его публичное имя в X-Forwarded-Host не источник.
	if code := post("10.0.0.5:5000", "https://mcprig.example", "mcprig.example"); code != http.StatusForbidden {
		t.Fatalf("пир вне списка: X-Forwarded-Host принят, код %d", code)
	}
	// Заголовок просто игнорируется: с Origin, равным Host, запрос проходит.
	if code := post("10.0.0.5:5000", "http://127.0.0.1:9088", "evil.example"); code != http.StatusOK {
		t.Fatalf("пир вне списка: верный Origin отвергнут, код %d", code)
	}
	// Пир из списка и loopback: заголовок учитывается, как раньше.
	if code := post("10.0.0.1:5000", "https://mcprig.example", "mcprig.example"); code != http.StatusOK {
		t.Fatalf("пир из списка: X-Forwarded-Host отвергнут, код %d", code)
	}
	if code := post("127.0.0.1:5000", "https://mcprig.example", "mcprig.example"); code != http.StatusOK {
		t.Fatalf("loopback: X-Forwarded-Host отвергнут, код %d", code)
	}

	spoof := func(remote string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "evil.internal:9088"
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-Host", "mcprig.example")
		req.Header.Set("X-MCP-Proxy-Secret", secret)
		return req
	}
	// Host не разрешён, публичное имя — только в заголовке.
	if e.m.hostAllowed(spoof("10.0.0.5:5000")) {
		t.Fatal("хост принят по заголовку от пира вне списка")
	}
	if !e.m.hostAllowed(spoof("10.0.0.1:5000")) {
		t.Fatal("хост из заголовка пира из списка отвергнут")
	}
	if got := e.m.requestAuthorities(spoof("10.0.0.5:5000")); len(got) != 1 {
		t.Fatalf("пир вне списка дал лишний authority: %+v", got)
	}
}

// SEC-08: пустой список доверенных пиров — только loopback (прокси на этой же
// машине); кривая запись валит старт, а не сужает доверие молча.
func TestTrustedProxiesDefaultAndParse(t *testing.T) {
	a, err := newAuth(AuthOptions{Off: true, ProxySecret: "s"})
	if err != nil {
		t.Fatalf("newAuth: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:5000"
	if a.trustsProxy(req) {
		t.Fatal("пустой список: доверие не-loopback пиру")
	}
	req.RemoteAddr = "[::1]:5000"
	req.Header.Set("x-mcp-proxy-secret", "s")
	if !a.trustsProxy(req) {
		t.Fatal("loopback обязан быть доверенным")
	}
	// S-05: адрес пира сам по себе доверия не даёт — без секрета forwarded-
	// заголовки не действуют, иначе локальный процесс обходил бы лок-аут входа.
	req.Header.Del("x-mcp-proxy-secret")
	if a.trustsProxy(req) {
		t.Fatal("loopback без секрета признан прокси")
	}
	for _, bad := range []string{"10.0.0.300", "10.0.0.0/33"} {
		if _, err := newAuth(AuthOptions{Off: true, ProxySecret: "s", TrustedProxies: bad}); err == nil {
			t.Fatalf("кривая запись MCPRIG_TRUSTED_PROXIES %q принята", bad)
		}
	}
}
