package manager

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/downstream"
)

// stdio-сервер для probe получает whitelist, а не окружение менеджера: иначе
// его процесс видит MCPRIG_PASSWORD и токен OpenBao.
func TestProbeEnvHasNoManagerSecrets(t *testing.T) {
	t.Setenv("MCPRIG_PASSWORD", "manager-password")
	t.Setenv("OPENBAO_TOKEN", "bao-token")
	t.Setenv("BAO_TOKEN", "bao-token-2")
	out := filepath.Join(t.TempDir(), "env.txt")
	sec := &Secrets{local: &envFile{values: map[string]string{"REF": "ref-value"}}}
	srv := &config.Server{
		Name:      "probe",
		Transport: config.TransportStdio,
		Command:   []string{"sh", "-c", "env > " + out + "; exit 1"},
		Env:       map[string]string{"OWN": "${REF}"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := &downstream.Connector{Version: "test", Env: managerEnv(sec)}
	if _, err := conn.Connect(ctx, expandServer(srv, sec)); err == nil {
		t.Fatal("сервер не завершился: ожидалась ошибка handshake")
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("сервер не записал env: %v", err)
	}
	env := string(body)
	if !strings.Contains(env, "OWN=ref-value") {
		t.Errorf("в env нет переменных самого сервера: %s", env)
	}
	for _, line := range strings.Split(env, "\n") {
		for _, bad := range []string{"MCPRIG_", "OPENBAO_TOKEN=", "BAO_TOKEN="} {
			if strings.Contains(line, bad) {
				t.Errorf("в env сервера утекло %s: %s", bad, line)
			}
		}
	}
}

// Пустой MCPRIG_ALLOWED_HOSTS — только loopback, а не «любой Host».
func TestAllowedHostsDefaultLoopback(t *testing.T) {
	m, err := New(Options{
		DataDir: t.TempDir(), Version: "test", CLIPath: fakeBin,
		Auth: AuthOptions{Off: true},
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)
	for host, want := range map[string]int{
		"127.0.0.1:9091": http.StatusOK,
		"localhost":      http.StatusOK,
		"[::1]:9091":     http.StatusOK,
		"evil.example":   http.StatusForbidden,
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		r.Host = host
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q: код %d, ожидался %d", host, w.Code, want)
		}
	}
}

func TestAllowedHostsWildcard(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true, AllowedHosts: []string{"*"}})
	r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	r.Host = "evil.example"
	w := httptest.NewRecorder()
	e.m.ServeHTTP(w, r)
	status(t, w, http.StatusOK)
}

// Шестая неудачная попытка за минуту — 429, успешный вход счётчик обнуляет.
func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t, AuthOptions{Password: "pw"})
	bad := map[string]any{"user": "admin", "password": "bad"}
	for i := 0; i < loginMaxFails; i++ {
		w := e.do(t, http.MethodPost, "/api/login", bad)
		status(t, w, http.StatusUnauthorized)
	}
	w := e.do(t, http.MethodPost, "/api/login", bad)
	status(t, w, http.StatusTooManyRequests)
	if retry := w.Header().Get("Retry-After"); retry == "" || retry == "0" {
		t.Errorf("нет Retry-After: %q", retry)
	}

	// Успех обнуляет счётчик: после него снова пять 401 и только шестая — 429.
	e2 := newEnv(t, AuthOptions{Password: "pw"})
	for i := 0; i < loginMaxFails-1; i++ {
		status(t, e2.do(t, http.MethodPost, "/api/login", bad), http.StatusUnauthorized)
	}
	status(t, e2.do(t, http.MethodPost, "/api/login", map[string]any{"user": "admin", "password": "pw"}), http.StatusOK)
	for i := 0; i < loginMaxFails; i++ {
		status(t, e2.do(t, http.MethodPost, "/api/login", bad), http.StatusUnauthorized)
	}
	status(t, e2.do(t, http.MethodPost, "/api/login", bad), http.StatusTooManyRequests)
}

// Редирект OpenBao на чужой хост не должен уносить X-Vault-Token.
func TestBaoRedirectDoesNotLeakToken(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Vault-Token") != ""
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/secret/data/mcp/x", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	b := &bao{
		addr: srv.URL, token: "token", mount: "secret", prefix: "mcp",
		client: newBaoClient(),
	}
	if _, err := b.get("x"); err == nil {
		t.Fatal("редирект на другой хост должен вернуть ошибку")
	}
	if leaked {
		t.Fatal("X-Vault-Token ушёл на другой хост")
	}
}
