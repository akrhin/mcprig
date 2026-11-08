package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin — фейковый «прокси» набора, собранный один раз на пакет.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "manager-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	fakeBin = filepath.Join(dir, "fakeset")
	cmd := exec.Command("go", "build", "-o", fakeBin, "./testdata/fakeset")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "сборка фейкового набора: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

type env struct {
	m   *Manager
	dir string
}

func newEnv(t *testing.T, auth AuthOptions) *env {
	t.Helper()
	dir := t.TempDir()
	// httptest.NewRequest ставит Host example.com: пустой список теперь значит
	// только loopback, поэтому тестовому менеджеру нужен явный хост.
	if auth.AllowedHosts == nil {
		auth.AllowedHosts = []string{"example.com"}
	}
	m, err := New(Options{
		DataDir: dir,
		Version: "test",
		CLIPath: fakeBin,
		Auth:    auth,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)
	return &env{m: m, dir: dir}
}

// do шлёт запрос в менеджер. body == nil — без тела.
func (e *env) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return e.doWithCookie(t, method, path, body, nil)
}

func (e *env) doWithCookie(t *testing.T, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	switch v := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	e.m.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("разбор ответа %d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func status(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("код = %d, ожидался %d; тело: %s", w.Code, want, w.Body.String())
	}
}

// addServer заводит сервер и возвращает его имя.
func (e *env) addServer(t *testing.T, name string) {
	t.Helper()
	w := e.do(t, http.MethodPost, "/api/servers", map[string]any{
		"name": name, "transport": "stdio", "command": []string{"/bin/true"},
	})
	status(t, w, http.StatusCreated)
}

// addSet заводит набор и возвращает его id.
func (e *env) addSet(t *testing.T, name string, servers ...string) string {
	t.Helper()
	w := e.do(t, http.MethodPost, "/api/sets", map[string]any{"name": name, "servers": servers})
	status(t, w, http.StatusCreated)
	return decode[struct {
		ID string `json:"id"`
	}](t, w).ID
}

func TestAuthFailClosed(t *testing.T) {
	if _, err := New(Options{DataDir: t.TempDir(), Auth: AuthOptions{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}); err == nil {
		t.Fatal("без пароля и без MCPRIG_AUTH=off менеджер обязан не стартовать")
	} else if !strings.Contains(err.Error(), "API открыт") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
}

func TestHealthzBeforeAuth(t *testing.T) {
	e := newEnv(t, AuthOptions{Password: "pw"})

	w := e.do(t, http.MethodGet, "/healthz", nil)
	status(t, w, http.StatusOK)
	var healthz struct {
		Status string `json:"status"`
		Sets   int    `json:"sets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &healthz); err != nil || healthz.Status != "ok" || healthz.Sets != 0 {
		t.Fatalf("healthz = %q", w.Body.String())
	}

	w = e.do(t, http.MethodGet, "/api/state", nil)
	status(t, w, http.StatusUnauthorized)
	if !decode[map[string]any](t, w)["login"].(bool) {
		t.Fatalf("401 без login:true: %s", w.Body.String())
	}
}

func TestLoginLogout(t *testing.T) {
	e := newEnv(t, AuthOptions{Password: "pw"})

	w := e.do(t, http.MethodPost, "/api/login", map[string]any{"user": "admin", "password": "bad"})
	status(t, w, http.StatusUnauthorized)

	w = e.do(t, http.MethodPost, "/api/login", map[string]any{"user": "admin", "password": "pw"})
	status(t, w, http.StatusOK)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie || !cookies[0].HttpOnly {
		t.Fatalf("ожидался HttpOnly-cookie %s, получено %v", sessionCookie, cookies)
	}
	cookie := cookies[0]

	w = e.doWithCookie(t, http.MethodGet, "/api/state", nil, cookie)
	status(t, w, http.StatusOK)

	w = e.doWithCookie(t, http.MethodPost, "/api/logout", nil, cookie)
	status(t, w, http.StatusOK)

	w = e.doWithCookie(t, http.MethodGet, "/api/state", nil, cookie)
	status(t, w, http.StatusUnauthorized)
}

func TestRouteErrors(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})

	status(t, e.do(t, http.MethodGet, "/api/nope", nil), http.StatusNotFound)

	w := e.do(t, http.MethodPost, "/api/state", nil)
	status(t, w, http.StatusMethodNotAllowed)
	if allow := w.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q", allow)
	}

	status(t, e.do(t, http.MethodPut, "/api/sets", nil), http.StatusMethodNotAllowed)
}

func TestEnvValueMustBeString(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	w := e.do(t, http.MethodPost, "/api/servers", `{"name":"a","transport":"stdio","command":["/bin/true"],"env":{"A":1}}`)
	status(t, w, http.StatusBadRequest)

	status(t, e.do(t, http.MethodPost, "/api/servers", "не json"), http.StatusBadRequest)
}

func TestPortOutOfRange(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")

	w := e.do(t, http.MethodPost, "/api/sets", map[string]any{"name": "s", "servers": []string{"a"}, "port": 9200})
	status(t, w, http.StatusBadRequest)
}

func TestSetsHideToken(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "s", "a")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/keys", nil)
	status(t, w, http.StatusOK)
	token := decode[struct {
		Token string `json:"token"`
	}](t, w).Token
	if len(token) != 48 {
		t.Fatalf("токен = %q", token)
	}

	w = e.do(t, http.MethodGet, "/api/sets", nil)
	status(t, w, http.StatusOK)
	body := w.Body.String()
	if strings.Contains(body, token) {
		t.Fatal("GET /api/sets отдал полный токен")
	}
	if !strings.Contains(body, token[:8]) {
		t.Fatalf("в ответе нет префикса ключа: %s", body)
	}
}

func TestImportExport(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})

	w := e.do(t, http.MethodPost, "/api/import", map[string]any{
		"mcpServers": map[string]any{"imp1": map[string]any{"command": []string{"/bin/true"}}},
	})
	status(t, w, http.StatusOK)
	added := decode[struct {
		Added []string `json:"added"`
	}](t, w).Added
	if len(added) != 1 || added[0] != "imp1" {
		t.Fatalf("added = %v", added)
	}

	w = e.do(t, http.MethodGet, "/api/export/registry", nil)
	status(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "imp1") || strings.Contains(w.Body.String(), "token") {
		t.Fatalf("экспорт реестра: %s", w.Body.String())
	}

	id := e.addSet(t, "s", "imp1")
	w = e.do(t, http.MethodGet, "/api/export?set="+id, nil)
	status(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "mcpServers") {
		t.Fatalf("экспорт набора: %s", w.Body.String())
	}

	status(t, e.do(t, http.MethodGet, "/api/export", nil), http.StatusBadRequest)
	status(t, e.do(t, http.MethodGet, "/api/export?set=nope", nil), http.StatusNotFound)
}

func TestStartStopFakeSet(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "run", "fake")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	if !decode[RuntimeStatus](t, w).Running {
		t.Fatalf("набор не запустился: %s", w.Body.String())
	}

	w = e.do(t, http.MethodGet, "/api/sets/"+id+"/logs", nil)
	status(t, w, http.StatusOK)

	w = e.do(t, http.MethodPost, "/api/sets/"+id+"/stop", nil)
	status(t, w, http.StatusOK)
	if decode[RuntimeStatus](t, w).Running {
		t.Fatalf("набор не остановился: %s", w.Body.String())
	}
}

func TestBodyErrors(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})

	w := e.do(t, http.MethodPost, "/api/servers", "не json")
	status(t, w, http.StatusBadRequest)
	if got := decode[map[string]string](t, w)["error"]; got != "ожидался JSON" {
		t.Fatalf("ошибка = %q", got)
	}

	big := `{"name":"a","description":"` + strings.Repeat("x", 1<<20) + `"}`
	status(t, e.do(t, http.MethodPost, "/api/servers", big), http.StatusBadRequest)
}

func TestCSRFAndHost(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true, AllowedHosts: []string{"allowed.example"}})

	r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	r.Host = "evil.example"
	w := httptest.NewRecorder()
	e.m.ServeHTTP(w, r)
	status(t, w, http.StatusForbidden)

	r = httptest.NewRequest(http.MethodGet, "/api/state", nil)
	r.Host = "allowed.example"
	w = httptest.NewRecorder()
	e.m.ServeHTTP(w, r)
	status(t, w, http.StatusOK)

	csrf := func(origin, site string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"name":"z"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Host = "allowed.example"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		e.m.ServeHTTP(w, r)
		return w.Code
	}
	if got := csrf("http://evil.example", ""); got != http.StatusForbidden {
		t.Fatalf("чужой Origin: код %d", got)
	}
	if got := csrf("", "cross-site"); got != http.StatusForbidden {
		t.Fatalf("cross-site: код %d", got)
	}
	if got := csrf("http://allowed.example", "same-origin"); got == http.StatusForbidden {
		t.Fatal("свой Origin отбит")
	}
}

func TestSetIgnoresClientIDAndToken(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")

	w := e.do(t, http.MethodPost, "/api/sets", map[string]any{
		"id": "chosen", "token": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"name": "s", "servers": []string{"a"},
	})
	status(t, w, http.StatusCreated)
	got := decode[struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}](t, w)
	if got.ID != "s" {
		t.Fatalf("id навязан клиентом: %q", got.ID)
	}
	if got.Token != "" {
		t.Fatalf("клиентский token принят: %q", got.Token)
	}
}

// Реестр переживает перезапуск менеджера — набор, созданный через API, виден
// новому менеджеру на том же каталоге.
func TestRegistrySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m1, err := New(Options{DataDir: dir, Auth: AuthOptions{Off: true, AllowedHosts: []string{"example.com"}}, CLIPath: fakeBin, Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := `{"name":"persist","transport":"stdio","command":["/bin/true"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m1.ServeHTTP(w, req)
	status(t, w, http.StatusCreated)
	m1.Close()

	m2, err := New(Options{DataDir: dir, Auth: AuthOptions{Off: true, AllowedHosts: []string{"example.com"}}, CLIPath: fakeBin, Log: log})
	if err != nil {
		t.Fatalf("повторный New: %v", err)
	}
	defer m2.Close()
	if m2.store.Server("persist") == nil {
		t.Fatal("сервер не пережил перезапуск менеджера")
	}
	if len(m2.store.Sets()) != 0 {
		t.Fatalf("лишние наборы: %v", m2.store.Sets())
	}
}

// addServerWithEnv заводит stdio-сервер с ссылками на ${VAR}.
func (e *env) addServerWithEnv(t *testing.T, name string, vars map[string]string) {
	t.Helper()
	w := e.do(t, http.MethodPost, "/api/servers", map[string]any{
		"name": name, "transport": "stdio", "command": []string{"/bin/true"}, "env": vars,
	})
	status(t, w, http.StatusCreated)
}

// Правка переменной перезапускает только наборы, чьи серверы на неё ссылаются.
func TestEnvPutRestartsOnlySetsUsingVar(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServerWithEnv(t, "db", map[string]string{"PASSWORD": "${DB_PASSWORD}"})
	e.addServer(t, "other")
	dbSet := e.addSet(t, "db", "db")
	otherSet := e.addSet(t, "other", "other")
	// Оба «работают»: проверяется выбор набора, а не старт — иначе тест
	// ловит чужой процесс на порту 91xx из параллельного пакета.
	for _, id := range []string{dbSet, otherSet} {
		markRunning(e.m, id, e.m.store.Set(id).Port)
	}
	e.m.rt.mu.Lock()
	dbProc, otherProc := e.m.rt.procs[dbSet], e.m.rt.procs[otherSet]
	e.m.rt.mu.Unlock()

	w := e.do(t, http.MethodPut, "/api/env/DB_PASSWORD", map[string]any{"value": "s3cret"})
	status(t, w, http.StatusOK)

	e.m.rt.mu.Lock()
	gotDB, gotOther := e.m.rt.procs[dbSet], e.m.rt.procs[otherSet]
	e.m.rt.mu.Unlock()
	if gotDB == dbProc {
		t.Fatal("набор со ссылкой на переменную не перезапускался")
	}
	if gotOther != otherProc {
		t.Fatal("набор без ссылки на переменную перезапущен")
	}
}

// Набор с id вне шаблона отбрасывается при загрузке — но не молча.
func TestNewLogsDroppedSets(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":1,"nextPort":9100,"servers":[{"name":"fs","command":["x"]}],"sets":[
		{"id":"Demo Set","name":"Demo Set","servers":["fs"],"port":9100}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "registry.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	m, err := New(Options{
		DataDir: dir, Version: "test", CLIPath: fakeBin,
		Auth: AuthOptions{Off: true},
		Log:  slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)

	if m.store.Set("Demo Set") != nil {
		t.Fatal("набор Demo Set не отброшен")
	}
	if !strings.Contains(buf.String(), "набор выброшен") {
		t.Fatalf("предупреждения в логе нет: %s", buf.String())
	}
}

// Явный порт проверяется в системе: занятый процессом — 409, а не 200.
func TestUpdateSetRejectsBusyPort(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "s", "a")

	ln, err := net.Listen("tcp", "127.0.0.1:9150")
	if err != nil {
		t.Fatalf("занять порт 9150: %v", err)
	}
	defer ln.Close()

	w := e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{"port": 9150})
	status(t, w, http.StatusConflict)
	if msg := decode[map[string]string](t, w)["error"]; !strings.Contains(msg, "занят процессом") {
		t.Fatalf("ошибка = %q", msg)
	}
}

// Перезапуск сервера внутри набора не существует: остаётся наборный.
func TestServerRestartRouteRemoved(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "s", "a")

	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/servers/a/restart", nil), http.StatusNotFound)
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/restart", nil), http.StatusOK)
}

// Удаление набора убирает каталог с servers.json (в нём bearer-ключ).
func TestDeleteSetRemovesDir(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "s", "a")
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil), http.StatusOK)

	dir := filepath.Join(e.dir, "sets", id)
	if _, err := os.Stat(filepath.Join(dir, "servers.json")); err != nil {
		t.Fatalf("servers.json не создан: %v", err)
	}
	status(t, e.do(t, http.MethodDelete, "/api/sets/"+id, nil), http.StatusOK)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("каталог удалённого набора остался: %v", err)
	}
}

