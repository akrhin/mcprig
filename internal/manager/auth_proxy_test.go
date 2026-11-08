package manager

import (
	"net/http/httptest"
	"testing"
)

func TestSecureRequestSeesProxyTLS(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if secureRequest(r) {
		t.Fatalf("без TLS и без заголовка cookie не Secure")
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if !secureRequest(r) {
		t.Fatalf("прокси сообщил https — cookie обязана быть Secure")
	}
}

func TestClientIPFollowsHeaderOnlyForProxy(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.RemoteAddr = "10.0.0.5:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")

	if got := (&auth{}).clientIP(r); got != "10.0.0.5" {
		t.Fatalf("без прокси заголовок подделывается: %q", got)
	}
	// Клиент прислал "1.2.3.4", прокси дописал свой адрес — считаем по нему.
	if got := (&auth{proxySecret: "s"}).clientIP(r); got != "5.6.7.8" {
		t.Fatalf("за прокси нужен адрес, дописанный прокси: %q", got)
	}
}
