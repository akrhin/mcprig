package manager

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestCallbackURLFromPublicURL: заданный publicUrl — единственный источник
// redirect, префикс пути сохраняется.
func TestCallbackURLFromPublicURL(t *testing.T) {
	cases := map[string]string{
		"https://panel.example":          "https://panel.example/oauth/callback",
		"https://panel.example/mcprig":   "https://panel.example/mcprig/oauth/callback",
		"http://192.168.1.92:9088/proxy": "http://192.168.1.92:9088/proxy/oauth/callback",
	}
	for publicURL, want := range cases {
		got, err := CallbackURL(publicURL, "127.0.0.1", 9091)
		if err != nil {
			t.Fatalf("%s: %v", publicURL, err)
		}
		if got != want {
			t.Fatalf("publicUrl %q: получено %q, ожидалось %q", publicURL, got, want)
		}
	}
}

// TestCallbackURLUnchangedWithoutPublicURL: без publicUrl адрес собирается
// как раньше — Host панели и её порт.
func TestCallbackURLUnchangedWithoutPublicURL(t *testing.T) {
	got, err := CallbackURL("", "192.168.1.92", 9088)
	if err != nil {
		t.Fatalf("CallbackURL: %v", err)
	}
	if want := "http://192.168.1.92:9088/oauth/callback"; got != want {
		t.Fatalf("получено %q, ожидалось %q", got, want)
	}
}

// TestXFHIgnoredWithoutProxySecret: без MCPRIG_PROXY_SECRET X-Forwarded-Host не
// публичное имя — он не добавляет authority и не проходит проверку Host.
// С секретом подделанный заголовок принимается: это и есть цена доверия —
// порт панели не должен быть доступен в обход прокси.
func TestXFHIgnoredWithoutProxySecret(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "192.168.1.92:9088"
	req.Header.Set("X-Forwarded-Host", "mcprig.example")

	plain := newEnv(t, AuthOptions{Off: true, AllowedHosts: []string{"192.168.1.92", "mcprig.example"}})
	if got := plain.m.requestAuthorities(req); len(got) != 1 {
		t.Fatalf("без секрета X-Forwarded-Host дал authority: %+v", got)
	}
	spoofed := httptest.NewRequest(http.MethodGet, "/", nil)
	spoofed.Host = "evil.example"
	spoofed.Header.Set("X-Forwarded-Host", "mcprig.example")
	if plain.m.hostAllowed(spoofed) {
		t.Fatal("без секрета подделанный X-Forwarded-Host прошёл проверку Host")
	}

	trusted := newEnv(t, AuthOptions{Off: true, ProxySecret: strings.Repeat("s", 32), AllowedHosts: []string{"192.168.1.92", "mcprig.example"}})
	if got := trusted.m.requestAuthorities(req); len(got) != 2 {
		t.Fatalf("с секретом ожидались Host и X-Forwarded-Host, получено %+v", got)
	}
	if !trusted.m.hostAllowed(spoofed) {
		// Ожидаемо: секрет означает «верю прокси», а не «проверяю прокси».
		t.Fatal("с секретом публичное имя из X-Forwarded-Host отвергнуто")
	}
}

// TestSecureCookieBehindProxy: публичный https при запросе по http всё равно
// ставит cookie Secure — иначе за TLS-прокси она уйдёт открытой.
func TestSecureCookieBehindProxy(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := New(Options{
		DataDir:   t.TempDir(),
		Host:      "127.0.0.1",
		Port:      9088,
		PublicURL: "https://panel.example/mcprig",
		Version:   "test",
		CLIPath:   fakeBin,
		Auth:      AuthOptions{Off: true, AllowedHosts: []string{"example.com"}},
		Log:       log,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/login", nil) // r.TLS == nil: до Go дошёл http
	if _, err := m.auth.startSession(w, r); err != nil {
		t.Fatalf("startSession: %v", err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name != sessionCookie {
			continue
		}
		if !c.Secure {
			t.Fatalf("cookie сессии без Secure при публичном https: %v", c)
		}
		return
	}
	t.Fatalf("cookie %s не поставлена: %v", sessionCookie, w.Result().Cookies())
}

// TestTLSRequiresBothFiles: TLS панели задаётся только парой, а файлы читаются
// на старте — иначе панель молча ушла бы на http.
func TestTLSRequiresBothFiles(t *testing.T) {
	base := Options{DataDir: t.TempDir(), Version: "test", CLIPath: fakeBin, Auth: AuthOptions{Off: true}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	base.Log = log

	onlyCert := base
	onlyCert.TLSCert = "/nonexistent/cert.pem"
	if _, err := New(onlyCert); err == nil {
		t.Fatal("один cert без key принят")
	}
	onlyKey := base
	onlyKey.TLSKey = "/nonexistent/key.pem"
	if _, err := New(onlyKey); err == nil {
		t.Fatal("один key без cert принят")
	}
	missing := base
	missing.TLSCert = filepath.Join(t.TempDir(), "cert.pem")
	missing.TLSKey = filepath.Join(t.TempDir(), "key.pem")
	if _, err := New(missing); err == nil {
		t.Fatal("отсутствующие файлы сертификата не отбили старт")
	}
}

// TestSecureOptionPreserved: явный Auth.Secure не затирается при старте, а без
// него и без TLS/https-publiсUrl cookie остаётся незащищённой.
func TestSecureOptionPreserved(t *testing.T) {
	cookie := func(secure bool, publicURL string) *http.Cookie {
		t.Helper()
		m, err := New(Options{
			DataDir:   t.TempDir(),
			Host:      "127.0.0.1",
			Port:      9088,
			PublicURL: publicURL,
			Version:   "test",
			CLIPath:   fakeBin,
			Auth:      AuthOptions{Off: true, Secure: secure},
			Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(m.Close)
		w := httptest.NewRecorder()
		if _, err := m.auth.startSession(w, httptest.NewRequest(http.MethodPost, "/api/login", nil)); err != nil {
			t.Fatalf("startSession: %v", err)
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatal("cookie сессии не поставлена")
		return nil
	}

	if c := cookie(true, ""); !c.Secure {
		t.Fatalf("Auth.Secure=true потерян: %v", c)
	}
	if c := cookie(false, ""); c.Secure {
		t.Fatalf("http без TLS получил Secure: %v", c)
	}
}
