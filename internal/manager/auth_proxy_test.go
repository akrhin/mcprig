package manager

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSecureRequestSeesProxyTLS(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if secureRequest(r, false) {
		t.Fatalf("без TLS и без заголовка cookie не Secure")
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	// Без MCPRIG_PROXY_SECRET заголовок не доверяется: клиент подделал бы https.
	if secureRequest(r, false) {
		t.Fatalf("X-Forwarded-Proto без доверенного прокси принят")
	}
	if !secureRequest(r, true) {
		t.Fatalf("прокси сообщил https — cookie обязана быть Secure")
	}
}

func TestClientIPFollowsHeaderOnlyForProxy(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.RemoteAddr = "10.0.0.5:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	// Прокси предъявляет секрет: без него адрес из заголовка не берётся даже
	// с loopback-пира, иначе локальный процесс обходил бы лимит входа.
	r.Header.Set("X-MCP-Proxy-Secret", "s")

	if got := (&auth{}).clientIP(r); got != "10.0.0.5" {
		t.Fatalf("без прокси заголовок подделывается: %q", got)
	}
	// Клиент прислал "1.2.3.4", прокси дописал свой адрес — считаем по нему.
	r.RemoteAddr = "127.0.0.1:5555" // прокси на этой же машине
	if got := (&auth{proxySecret: "s"}).clientIP(r); got != "5.6.7.8" {
		t.Fatalf("за прокси нужен адрес, дописанный прокси: %q", got)
	}
}

// loginAs — попытка входа с заданного сокетного адреса и X-Forwarded-For.
func loginAs(e *env, password, remote, xff string) int {
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"user":"admin","password":"`+password+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if e.secret != "" {
		req.Header.Set("X-MCP-Proxy-Secret", e.secret)
	}
	w := httptest.NewRecorder()
	e.m.ServeHTTP(w, req)
	return w.Code
}

// SEC-11: за доверенным прокси ключ лимита — адрес клиента из X-Forwarded-For
// (его дописал прокси), а не адрес сокета: пять неудач одного клиента не
// блокируют соседа за тем же прокси.
func TestLoginLimitKeysClientBehindTrustedProxy(t *testing.T) {
	secret := strings.Repeat("s", 32)
	e := newEnv(t, AuthOptions{Password: "right", ProxySecret: secret, TrustedProxies: "10.0.0.1"})

	for i := 0; i < loginMaxFails; i++ {
		if code := loginAs(e, "wrong", "10.0.0.1:5000", "203.0.113.10"); code != http.StatusUnauthorized {
			t.Fatalf("попытка %d: код %d", i+1, code)
		}
	}
	if code := loginAs(e, "wrong", "10.0.0.1:5000", "203.0.113.10"); code != http.StatusTooManyRequests {
		t.Fatalf("лимит по адресу клиента не сработал: код %d", code)
	}
	if code := loginAs(e, "wrong", "10.0.0.1:5000", "203.0.113.11"); code != http.StatusUnauthorized {
		t.Fatalf("сосед за тем же прокси заблокирован: код %d", code)
	}
}

// SEC-11: пир без доверия — X-Forwarded-For не ключ; чужие адреса в заголовке
// не блокируют нового клиента с другого сокета.
func TestLoginLimitIgnoresForwardedFromUntrustedPeer(t *testing.T) {
	secret := strings.Repeat("s", 32)
	e := newEnv(t, AuthOptions{Password: "right", ProxySecret: secret, TrustedProxies: "10.9.9.9"})

	for i := 0; i < loginMaxFails; i++ {
		if code := loginAs(e, "wrong", "10.0.1."+strconv.Itoa(i)+":5000", "198.51.100.7"); code != http.StatusUnauthorized {
			t.Fatalf("попытка %d: код %d", i+1, code)
		}
	}
	if code := loginAs(e, "wrong", "10.0.1.99:5000", "198.51.100.7"); code != http.StatusUnauthorized {
		t.Fatalf("XFF от недоверенного пира заблокировал нового клиента: код %d", code)
	}
}

// TestLoginSuccessResetsBothCounters: успешный вход чистит и сокетный ключ, и
// адрес прокси.
func TestLoginSuccessResetsBothCounters(t *testing.T) {
	secret := strings.Repeat("s", 32)
	e := newEnv(t, AuthOptions{Password: "right", ProxySecret: secret})

	for i := 0; i < loginMaxFails-1; i++ {
		if code := loginAs(e, "wrong", "10.0.0.2:5000", "203.0.113.1"); code != http.StatusUnauthorized {
			t.Fatalf("попытка %d: код %d", i+1, code)
		}
	}
	if code := loginAs(e, "right", "10.0.0.2:5000", "203.0.113.1"); code != http.StatusOK {
		t.Fatalf("верный пароль: код %d", code)
	}
	// Тот же сокет, другой адрес прокси: сокетный счётчик обязан быть сброшен.
	for i := 0; i < loginMaxFails-1; i++ {
		if code := loginAs(e, "wrong", "10.0.0.2:5000", "203.0.113.2"); code != http.StatusUnauthorized {
			t.Fatalf("после сброса попытка %d: код %d", i+1, code)
		}
	}
}

// TestLoginLimitWithoutSecretIgnoresHeader: без MCPRIG_PROXY_SECRET заголовок
// не источник ключа — считаем только по адресу сокета.
func TestLoginLimitWithoutSecretIgnoresHeader(t *testing.T) {
	e := newEnv(t, AuthOptions{Password: "right"})
	for i := 0; i < loginMaxFails; i++ {
		if code := loginAs(e, "wrong", "10.0.0.3:5000", "203.0.113."+strconv.Itoa(i)); code != http.StatusUnauthorized {
			t.Fatalf("попытка %d: код %d", i+1, code)
		}
	}
	if code := loginAs(e, "wrong", "10.0.0.3:5000", "203.0.113.99"); code != http.StatusTooManyRequests {
		t.Fatalf("без секрета лимит не сработал: код %d", code)
	}
}
