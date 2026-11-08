package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/registry"
)

const maxBody = 1 << 20

// Options — всё, что нужно менеджеру. FromEnv читает MCPRIG_*.
type Options struct {
	DataDir  string
	Host     string
	Port     int
	CLIPath  string // бинарник, который запускается как набор: `serve --config …`
	BindIP   string // transport.host в servers.json набора
	SeedFile string
	Version  string
	Auth     AuthOptions
	Log      *slog.Logger
}

func FromEnv(version string) Options {
	port := 9091
	if v := os.Getenv("MCPRIG_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}
	return Options{
		DataDir:  envOr(os.Getenv("MCPRIG_DATA_DIR"), defaultDataDir()),
		Host:     envOr(os.Getenv("MCPRIG_HOST"), "127.0.0.1"),
		Port:     port,
		CLIPath:  os.Getenv("MCPRIG_CLI"),
		BindIP:   envOr(os.Getenv("MCPRIG_BIND_IP"), "0.0.0.0"),
		SeedFile: os.Getenv("MCPRIG_SEED_FILE"),
		Version:  version,
		Auth: AuthOptions{
			Password:     os.Getenv("MCPRIG_PASSWORD"),
			PasswordHash: os.Getenv("MCPRIG_PASSWORD_HASH"),
			ProxySecret:  os.Getenv("MCPRIG_PROXY_SECRET"),
			User:         os.Getenv("MCPRIG_USER"),
			UserHeader:   os.Getenv("MCPRIG_USER_HEADER"),
			AllowedHosts: splitList(os.Getenv("MCPRIG_ALLOWED_HOSTS")),
			Off:          os.Getenv("MCPRIG_AUTH") == "off",
		},
	}
}

func defaultDataDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "mcprig")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/data"
	}
	return filepath.Join(home, ".local", "state", "mcprig")
}

// Manager — HTTP API и супервизия наборов.
type Manager struct {
	opts    Options
	store   *registry.Store
	rt      *Runtime
	sec     *Secrets
	auth    *auth
	log     *slog.Logger
	version string
	health  *healthCache
	browse  *browser
	routes  []route
}

func New(opts Options) (*Manager, error) {
	if opts.DataDir == "" {
		return nil, errors.New("менеджер: не задан каталог данных")
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, err
	}
	if opts.SeedFile != "" {
		// registry.Open берёт сид из окружения — другого входа у чужого пакета нет.
		os.Setenv("MANAGER_SEED_FILE", opts.SeedFile)
	}
	store, err := registry.Open(opts.DataDir)
	if err != nil {
		return nil, err
	}
	sec, err := NewSecrets(opts.DataDir)
	if err != nil {
		return nil, err
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	for _, id := range store.Dropped() {
		log.Warn("набор выброшен: некорректный id", "id", id)
	}
	a, err := newAuth(opts.Auth)
	if err != nil {
		return nil, err
	}
	if len(a.allowedHosts) == 0 {
		log.Warn("менеджер: MCPRIG_ALLOWED_HOSTS пуст — принимаются только loopback-хосты")
	}
	for _, host := range a.allowedHosts {
		if host == "*" {
			log.Warn("менеджер: MCPRIG_ALLOWED_HOSTS содержит \"*\" — защита от DNS rebinding отключена")
		}
	}
	if secret := opts.Auth.ProxySecret; secret != "" && len(secret) < 32 {
		log.Warn("менеджер: MCPRIG_PROXY_SECRET короче 32 символов")
	}
	cli := opts.CLIPath
	if cli == "" {
		if cli, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	version := envOr(opts.Version, "dev")
	m := &Manager{
		opts:    opts,
		store:   store,
		sec:     sec,
		auth:    a,
		log:     log,
		version: version,
		health:  newHealthCache(),
		browse:  newBrowser(sec, version),
	}
	m.rt = NewRuntime(opts.DataDir, cli, opts.BindIP, sec, log)
	m.rt.reload = func(id string) (*registry.Set, []registry.Server) {
		set := m.store.Set(id)
		if set == nil {
			return nil, nil
		}
		return set, m.store.Servers()
	}
	m.routes = m.buildRoutes()
	return m, nil
}

func (m *Manager) Handler() http.Handler { return m }

func (m *Manager) Close() {
	m.browse.closeAll()
	m.rt.Close()
}

// Autostart поднимает наборы с autostart: true. Ошибки только в лог.
func (m *Manager) Autostart() {
	for _, set := range m.store.Sets() {
		if !set.Autostart {
			continue
		}
		set := set
		go func() {
			if _, err := m.startSet(context.Background(), set); err != nil {
				m.log.Warn("автостарт набора не удался", "set", set.ID, "err", err)
			}
		}()
	}
}

// Run поднимает HTTP API и живёт до сигнала.
func Run(opts Options) error {
	m, err := New(opts)
	if err != nil {
		return err
	}
	defer m.Close()

	release, err := lockDataDir(opts.DataDir)
	if err != nil {
		return err
	}
	defer release()

	srv := &http.Server{
		Addr:              net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)),
		Handler:           m.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	m.log.Info("менеджер запущен", "addr", srv.Addr, "data", opts.DataDir, "auth", m.auth.mode)
	m.Autostart()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	m.log.Info("менеджер остановлен")
	return nil
}

// healthz публичный (нужен docker/systemd healthcheck), поэтому имён наборов
// в ответе нет: только статус и счётчик. Мёртвый autostart — 503.
func (m *Manager) healthz(w http.ResponseWriter) {
	sets := m.store.Sets()
	status, code := "ok", http.StatusOK
	for _, set := range sets {
		if set.Autostart && !m.rt.status(set).Running {
			status, code = "degraded", http.StatusServiceUnavailable
			break
		}
	}
	writeJSON(w, code, map[string]any{"status": status, "sets": len(sets)})
}

// lockDataDir не даёт двум менеджерам делить один реестр.
func lockDataDir(dir string) (func(), error) {
	path := filepath.Join(dir, "manager.pid")
	if body, err := os.ReadFile(path); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(body))); err == nil && pid > 0 && syscall.Kill(pid, 0) == nil {
			return nil, fmt.Errorf("менеджер уже работает (pid %d, %s)", pid, path)
		}
		_ = os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Close()
	return func() { _ = os.Remove(path) }, nil
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	defer func() {
		if rec := recover(); rec != nil {
			m.log.Error("паника в обработчике", "path", r.URL.Path, "panic", rec)
			m.fail(w, r, &httpError{http.StatusInternalServerError, "внутренняя ошибка"})
		}
	}()

	if !m.auth.hostOK(r.Host) {
		m.fail(w, r, &httpError{http.StatusForbidden, "хост не разрешён"})
		return
	}

	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			m.methodNotAllowed(w, []string{http.MethodGet})
			return
		}
		m.healthz(w)
		return
	}
	if err := checkCSRF(r); err != nil {
		m.fail(w, r, err)
		return
	}

	switch r.URL.Path {
	case "/login":
		m.handleLoginPage(w, r)
		return
	case "/api/login":
		m.handleLogin(w, r)
		return
	case "/api/logout":
		m.handleLogout(w, r)
		return
	}

	if _, ok := m.auth.check(r); !ok {
		m.unauthorized(w, r)
		return
	}
	if m.servePanel(w, r) {
		return
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/ui" || r.URL.Path == "/ui/") {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if err := m.serveRoute(w, r); err != nil {
		m.fail(w, r, err)
	}
}

// ─── auth-страницы ───────────────────────────────────────────────────────────

func (m *Manager) unauthorized(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/ui" || r.URL.Path == "/ui/") {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]any{"error": "нужна авторизация", "login": true})
}

func (m *Manager) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !m.auth.loginEnabled() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		m.methodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if _, ok := m.auth.check(r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	writeHTML(w, http.StatusOK, loginTemplate, loginData{})
}

func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !m.auth.loginEnabled() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		m.methodNotAllowed(w, []string{http.MethodPost})
		return
	}
	ip := m.auth.clientIP(r)
	if wait, blocked := m.auth.loginBlocked(ip); blocked {
		secs := int((wait + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		m.fail(w, r, &httpError{http.StatusTooManyRequests, "слишком много попыток входа — повторите позже"})
		return
	}
	user, password, isForm := "", "", false
	if ct := r.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			m.fail(w, r, &httpError{http.StatusBadRequest, "не удалось разобрать форму"})
			return
		}
		user, password, isForm = r.PostFormValue("user"), r.PostFormValue("password"), true
	} else {
		var body struct {
			User     string `json:"user"`
			Password string `json:"password"`
		}
		if err := decodeJSON(r, &body); err != nil {
			m.fail(w, r, err)
			return
		}
		user, password = body.User, body.Password
	}
	if !m.auth.loginCheck(user, password) {
		m.auth.loginFailed(ip)
		if isForm {
			writeHTML(w, http.StatusUnauthorized, loginTemplate, loginData{Error: "неверный логин или пароль"})
			return
		}
		m.fail(w, r, &httpError{http.StatusUnauthorized, "неверный логин или пароль"})
		return
	}
	m.auth.loginOK(ip)
	name, err := m.auth.startSession(w, r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if isForm {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": name})
}

func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		m.methodNotAllowed(w, []string{http.MethodPost})
		return
	}
	m.auth.clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ─── роутер ──────────────────────────────────────────────────────────────────

type request struct {
	w    http.ResponseWriter
	r    *http.Request
	args []string
}

type handlerFunc func(*request) error

type route struct {
	re      *regexp.Regexp
	methods map[string]handlerFunc
}

// setPattern — NAME_PATTERN из контракта.
const setPattern = `([a-z0-9][a-z0-9_-]{0,63})`

func (m *Manager) buildRoutes() []route {
	mk := func(pattern string, methods map[string]handlerFunc) route {
		return route{re: regexp.MustCompile(pattern), methods: methods}
	}
	return []route{
		mk(`^/api/state$`, map[string]handlerFunc{http.MethodGet: m.apiState}),
		mk(`^/api/calls$`, map[string]handlerFunc{http.MethodGet: m.apiCalls}),
		mk(`^/api/import$`, map[string]handlerFunc{http.MethodPost: m.apiImport}),
		mk(`^/api/export/registry$`, map[string]handlerFunc{http.MethodGet: m.apiExportRegistry}),
		mk(`^/api/export$`, map[string]handlerFunc{http.MethodGet: m.apiExportSet}),
		mk(`^/api/env$`, map[string]handlerFunc{http.MethodGet: m.apiEnvList}),
		mk(`^/api/env/([A-Za-z_][A-Za-z0-9_]*)$`, map[string]handlerFunc{
			http.MethodPut: m.apiEnvPut, http.MethodDelete: m.apiEnvDelete,
		}),
		mk(`^/api/servers/`+setPattern+`/probe$`, map[string]handlerFunc{http.MethodPost: m.apiProbe}),
		mk(`^/api/browse$`, map[string]handlerFunc{http.MethodPost: m.apiBrowseOpen}),
		mk(`^/api/browse/([0-9a-f]{16})$`, map[string]handlerFunc{
			http.MethodGet: m.apiBrowseGet, http.MethodDelete: m.apiBrowseClose,
		}),
		mk(`^/api/browse/([0-9a-f]{16})/(tool|resource|prompt)$`, map[string]handlerFunc{http.MethodPost: m.apiBrowseCall}),
		mk(`^/api/servers$`, map[string]handlerFunc{
			http.MethodGet: m.apiServersList, http.MethodPost: m.apiServerAdd,
		}),
		mk(`^/api/servers/`+setPattern+`$`, map[string]handlerFunc{
			http.MethodPut: m.apiServerUpdate, http.MethodDelete: m.apiServerDelete,
		}),
		mk(`^/api/sets/`+setPattern+`/keys$`, map[string]handlerFunc{http.MethodPost: m.apiKeyIssue}),
		mk(`^/api/sets/`+setPattern+`/keys/([A-Za-z0-9_-]{1,32})$`, map[string]handlerFunc{http.MethodDelete: m.apiKeyRevoke}),
		mk(`^/api/sets/`+setPattern+`/servers$`, map[string]handlerFunc{http.MethodGet: m.apiSetServers}),
		mk(`^/api/sets/`+setPattern+`/servers/`+setPattern+`/logs$`, map[string]handlerFunc{http.MethodGet: m.apiServerLogs}),
		mk(`^/api/sets/`+setPattern+`/servers/`+setPattern+`/restart$`, map[string]handlerFunc{http.MethodPost: m.apiServerRestart}),
		mk(`^/api/sets/`+setPattern+`/logs$`, map[string]handlerFunc{http.MethodGet: m.apiSetLogs}),
		mk(`^/api/sets/`+setPattern+`/(start|stop|restart)$`, map[string]handlerFunc{http.MethodPost: m.apiSetAction}),
		mk(`^/api/sets$`, map[string]handlerFunc{
			http.MethodGet: m.apiSetsList, http.MethodPost: m.apiSetAdd,
		}),
		mk(`^/api/sets/`+setPattern+`$`, map[string]handlerFunc{
			http.MethodPut: m.apiSetUpdate, http.MethodDelete: m.apiSetDelete,
		}),
	}
}

func (m *Manager) serveRoute(w http.ResponseWriter, r *http.Request) error {
	for _, rt := range m.routes {
		args := rt.re.FindStringSubmatch(r.URL.Path)
		if args == nil {
			continue
		}
		h, ok := rt.methods[r.Method]
		if !ok {
			m.methodNotAllowed(w, methodNames(rt.methods))
			return nil
		}
		return h(&request{w: w, r: r, args: args[1:]})
	}
	return &httpError{http.StatusNotFound, "not found"}
}

func methodNames(methods map[string]handlerFunc) []string {
	out := make([]string, 0, len(methods))
	for name := range methods {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) methodNotAllowed(w http.ResponseWriter, allowed []string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "метод не разрешён"})
}

// ─── ошибки ──────────────────────────────────────────────────────────────────

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(msg string) error { return &httpError{http.StatusBadRequest, msg} }
func notFound(msg string) error   { return &httpError{http.StatusNotFound, msg} }
func conflict(msg string) error   { return &httpError{http.StatusConflict, msg} }
func serverError(msg string) error {
	return &httpError{http.StatusInternalServerError, msg}
}

func (m *Manager) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := http.StatusBadRequest, "неверный запрос"
	var he *httpError
	var pathErr *fs.PathError
	switch {
	case errors.As(err, &he):
		status, msg = he.status, he.msg
	case errors.Is(err, errBrowseGone):
		status, msg = http.StatusNotFound, errBrowseGone.Error()
	case errors.As(err, &pathErr):
		status, msg = http.StatusInternalServerError, "внутренняя ошибка"
	default:
		msg = err.Error()
	}
	if status >= 500 {
		m.log.Error("ошибка API", "method", r.Method, "path", r.URL.Path, "status", status, "err", err)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func checkCSRF(r *http.Request) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return nil
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return &httpError{http.StatusForbidden, "запрос с чужого сайта"}
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return &httpError{http.StatusForbidden, "origin не совпадает с хостом"}
		}
	}
	if r.ContentLength != 0 && r.URL.Path != "/api/login" && !isJSON(r.Header.Get("Content-Type")) {
		return badRequest("ожидался JSON")
	}
	return nil
}

func isJSON(contentType string) bool {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return contentType == "application/json" || strings.HasSuffix(contentType, "+json")
}

// ─── тело и ответы ───────────────────────────────────────────────────────────

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, badRequest("не удалось прочитать тело")
	}
	if len(body) > maxBody {
		return nil, badRequest("тело запроса больше 1 МБ")
	}
	return body, nil
}

func decodeJSON(r *http.Request, v any) error {
	body, err := readBody(r)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return badRequest("ожидался JSON")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return badRequest("ожидался JSON")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// ─── представления ──────────────────────────────────────────────────────────

type setView struct {
	registry.PublicSetEntry
	Runtime RuntimeStatus `json:"runtime"`
}

func (m *Manager) setView(set *registry.Set) setView {
	return setView{PublicSetEntry: registry.PublicSet(set), Runtime: m.rt.status(set)}
}

func (m *Manager) setViews() []setView {
	sets := m.store.Sets()
	out := make([]setView, 0, len(sets))
	for _, set := range sets {
		out = append(out, m.setView(set))
	}
	return out
}

type envVar struct {
	Name   string `json:"name"`
	Set    bool   `json:"set"`
	Source string `json:"source,omitempty"`
}

// ─── state / import / export ─────────────────────────────────────────────────

func (m *Manager) apiState(req *request) error {
	writeJSON(req.w, http.StatusOK, map[string]any{
		"servers":   serversOrEmpty(m.store.Servers()),
		"sets":      m.setViews(),
		"firstPort": registry.FirstPort,
	})
	return nil
}

func (m *Manager) apiImport(req *request) error {
	var body struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := decodeJSON(req.r, &body); err != nil {
		return err
	}
	if body.MCPServers == nil {
		return badRequest("нужен объект mcpServers")
	}
	if name, found := importRefsReserved(body.MCPServers); found {
		return reservedRefError(name)
	}
	added, skipped := m.store.Import(body.MCPServers)
	if added == nil {
		added = []string{}
	}
	if skipped == nil {
		skipped = []registry.Skip{}
	}
	writeJSON(req.w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
	return nil
}

type seedSet struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Port        int      `json:"port"`
	Servers     []string `json:"servers"`
	Autostart   bool     `json:"autostart,omitempty"`
	Open        bool     `json:"open,omitempty"`
}

func (m *Manager) apiExportRegistry(req *request) error {
	sets := m.store.Sets()
	out := struct {
		Version int               `json:"version"`
		Servers []registry.Server `json:"servers"`
		Sets    []seedSet         `json:"sets"`
	}{
		Version: 1,
		Servers: serversOrEmpty(m.store.Servers()),
		Sets:    make([]seedSet, 0, len(sets)),
	}
	for _, set := range sets {
		out.Sets = append(out.Sets, seedSet{
			ID:          set.ID,
			Name:        set.Name,
			Description: set.Description,
			Port:        set.Port,
			Servers:     set.Servers,
			Autostart:   set.Autostart,
			Open:        set.Open,
		})
	}
	writeJSON(req.w, http.StatusOK, out)
	return nil
}

func (m *Manager) apiExportSet(req *request) error {
	id := req.r.URL.Query().Get("set")
	if id == "" {
		return badRequest("нужен параметр set")
	}
	if m.store.Set(id) == nil {
		return notFound("нет набора " + id)
	}
	body, err := m.store.ExportSet(id)
	if err != nil {
		return err
	}
	writeJSON(req.w, http.StatusOK, body)
	return nil
}

// ─── env ─────────────────────────────────────────────────────────────────────

func (m *Manager) apiEnvList(req *request) error {
	names := envRefs(m.store.Servers())
	vars := make([]envVar, 0, len(names))
	for _, name := range names {
		value, source := m.sec.Get(name)
		vars = append(vars, envVar{Name: name, Set: value != "", Source: source})
	}
	resp := map[string]any{"vars": vars}
	if remoteErr := m.sec.RemoteError(); remoteErr != "" {
		resp["remoteError"] = remoteErr
	}
	writeJSON(req.w, http.StatusOK, resp)
	return nil
}

func (m *Manager) apiEnvPut(req *request) error {
	name := req.args[0]
	var body struct {
		Value string `json:"value"`
		Store string `json:"store"`
	}
	if err := decodeJSON(req.r, &body); err != nil {
		return err
	}
	if body.Value == "" {
		return badRequest("нужно непустое значение")
	}
	source, err := m.sec.Set(name, body.Value, body.Store)
	if err != nil {
		return badRequest(err.Error())
	}
	restarted := m.restartSetsUsingEnv(name)
	writeJSON(req.w, http.StatusOK, map[string]any{
		"name": name, "set": true, "source": source, "restarted": restarted,
	})
	return nil
}

func (m *Manager) apiEnvDelete(req *request) error {
	name := req.args[0]
	removed, err := m.sec.Delete(name, req.r.URL.Query().Get("store"))
	if err != nil {
		return badRequest(err.Error())
	}
	restarted := m.restartSetsUsingEnv(name)
	writeJSON(req.w, http.StatusOK, map[string]any{
		"name": name, "removed": removed, "restarted": restarted,
	})
	return nil
}

// ─── probe / browse ──────────────────────────────────────────────────────────

func (m *Manager) apiProbe(req *request) error {
	name := req.args[0]
	srv := m.store.Server(name)
	if srv == nil {
		return notFound("нет сервера " + name)
	}
	writeJSON(req.w, http.StatusOK, m.probeServer(req.r.Context(), srv))
	return nil
}

func (m *Manager) apiBrowseOpen(req *request) error {
	var body struct {
		Server string          `json:"server"`
		Entry  json.RawMessage `json:"entry"`
	}
	if err := decodeJSON(req.r, &body); err != nil {
		return err
	}
	srv := m.store.Server(body.Server)
	if body.Server == "" && len(body.Entry) > 0 {
		var entry config.Server
		if err := json.Unmarshal(body.Entry, &entry); err != nil {
			return badRequest("entry: ожидался объект сервера")
		}
		if entry.Name == "" {
			return badRequest("entry: нужно имя сервера")
		}
		srv = &entry
	}
	if srv == nil {
		return badRequest("нужен server или entry")
	}
	view, err := m.browse.open(req.r.Context(), srv)
	if err != nil {
		return &httpError{http.StatusBadGateway, truncate(err.Error(), 800)}
	}
	writeJSON(req.w, http.StatusOK, view)
	return nil
}

func (m *Manager) apiBrowseGet(req *request) error {
	view, err := m.browse.get(req.args[0])
	if err != nil {
		return err
	}
	writeJSON(req.w, http.StatusOK, view)
	return nil
}

func (m *Manager) apiBrowseClose(req *request) error {
	if !m.browse.close(req.args[0]) {
		return errBrowseGone
	}
	writeJSON(req.w, http.StatusOK, map[string]any{"closed": true})
	return nil
}

func (m *Manager) apiBrowseCall(req *request) error {
	id, action := req.args[0], req.args[1]
	var body struct {
		Name string         `json:"name"`
		URI  string         `json:"uri"`
		Args map[string]any `json:"args"`
	}
	if err := decodeJSON(req.r, &body); err != nil {
		return err
	}
	var (
		result any
		err    error
	)
	switch action {
	case "tool":
		if body.Name == "" {
			return badRequest("нужно имя инструмента")
		}
		result, err = m.browse.callTool(req.r.Context(), id, body.Name, body.Args)
	case "resource":
		if body.URI == "" {
			return badRequest("нужен uri")
		}
		result, err = m.browse.readResource(req.r.Context(), id, body.URI)
	case "prompt":
		if body.Name == "" {
			return badRequest("нужно имя промпта")
		}
		promptArgs := make(map[string]string, len(body.Args))
		for k, v := range body.Args {
			if s, ok := v.(string); ok {
				promptArgs[k] = s
			} else {
				promptArgs[k] = fmt.Sprint(v)
			}
		}
		result, err = m.browse.getPrompt(req.r.Context(), id, body.Name, promptArgs)
	}
	if err != nil {
		if errors.Is(err, errBrowseGone) {
			return err
		}
		return &httpError{http.StatusBadGateway, truncate(err.Error(), 800)}
	}
	writeJSON(req.w, http.StatusOK, map[string]any{"result": result})
	return nil
}

// ─── servers ─────────────────────────────────────────────────────────────────

// checkReservedRefs отбивает сервер, который подставляет служебную ${VAR}
// менеджера: значение ушло бы в env набора или в URL при probe/browse.
func checkReservedRefs(raw json.RawMessage) error {
	var srv config.Server
	if err := json.Unmarshal(raw, &srv); err != nil {
		return nil // кривой JSON отклонит store
	}
	if names := reservedRefs(&srv); len(names) > 0 {
		return reservedRefError(names[0])
	}
	return nil
}

func reservedRefError(name string) error {
	return badRequest(fmt.Sprintf("переменная ${%s} — служебная, её нельзя подставлять в сервер", name))
}

// importRefsReserved ищет служебные ${VAR} в сырых записях mcpServers.
// Значения разбираются, а не ищутся текстом: имя может прийти с \u-escape.
func importRefsReserved(entries map[string]json.RawMessage) (string, bool) {
	for _, raw := range entries {
		seen := map[string]bool{}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		switch trimmed[0] {
		case '{':
			var srv config.Server
			if json.Unmarshal(trimmed, &srv) != nil {
				continue
			}
			for _, name := range serverEnvRefs(&srv) {
				seen[name] = true
			}
		case '"':
			var text string
			if json.Unmarshal(trimmed, &text) == nil {
				addRefs(seen, text)
			}
		case '[':
			var argv []string
			if json.Unmarshal(trimmed, &argv) == nil {
				for _, part := range argv {
					addRefs(seen, part)
				}
			}
		}
		for name := range seen {
			if reservedSecret(name) {
				return name, true
			}
		}
	}
	return "", false
}

func (m *Manager) apiServersList(req *request) error {
	writeJSON(req.w, http.StatusOK, serversOrEmpty(m.store.Servers()))
	return nil
}

func (m *Manager) apiServerAdd(req *request) error {
	body, err := readBody(req.r)
	if err != nil {
		return err
	}
	if len(body) == 0 || !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		return badRequest("ожидался JSON")
	}
	if err := checkReservedRefs(body); err != nil {
		return err
	}
	var name struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(body, &name)
	if name.Name != "" && m.store.Server(name.Name) != nil {
		return conflict("сервер " + name.Name + " уже есть")
	}
	srv, err := m.store.AddServer(json.RawMessage(body))
	if err != nil {
		if strings.Contains(err.Error(), "уже есть") {
			return conflict(err.Error())
		}
		return err
	}
	writeJSON(req.w, http.StatusCreated, srv)
	return nil
}

func (m *Manager) apiServerUpdate(req *request) error {
	name := req.args[0]
	if m.store.Server(name) == nil {
		return notFound("нет сервера " + name)
	}
	body, err := readBody(req.r)
	if err != nil {
		return err
	}
	if err := checkReservedRefs(body); err != nil {
		return err
	}
	srv, err := m.store.UpdateServer(name, json.RawMessage(body))
	if err != nil {
		return err
	}
	restarted := m.restartSetsUsing(name)
	raw, err := json.Marshal(srv)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	out["restarted"] = restarted
	writeJSON(req.w, http.StatusOK, out)
	return nil
}

func (m *Manager) apiServerDelete(req *request) error {
	name := req.args[0]
	if m.store.Server(name) == nil {
		return notFound("нет сервера " + name)
	}
	stopped := m.stopSetsUsing(name)
	if err := m.store.DeleteServer(name); err != nil {
		return err
	}
	writeJSON(req.w, http.StatusOK, map[string]any{"ok": true, "stopped": stopped})
	return nil
}

// ─── sets ────────────────────────────────────────────────────────────────────

func (m *Manager) apiSetsList(req *request) error {
	writeJSON(req.w, http.StatusOK, m.setViews())
	return nil
}

func (m *Manager) apiSetAdd(req *request) error {
	body, err := readBody(req.r)
	if err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if len(body) == 0 || json.Unmarshal(body, &obj) != nil {
		return badRequest("ожидался JSON")
	}
	// id и token не принимаются от клиента: и порт, и ключ выдаёт менеджер.
	delete(obj, "id")
	delete(obj, "token")
	raw, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	set, err := m.store.AddSet(raw)
	if err != nil {
		if strings.Contains(err.Error(), "уже есть") || strings.Contains(err.Error(), "занят") {
			return conflict(err.Error())
		}
		return err
	}
	writeJSON(req.w, http.StatusCreated, m.setView(set))
	return nil
}

func (m *Manager) apiSetUpdate(req *request) error {
	id := req.args[0]
	old := m.store.Set(id)
	if old == nil {
		return notFound("нет набора " + id)
	}
	body, err := readBody(req.r)
	if err != nil {
		return err
	}
	running := m.rt.status(old).Running
	set, err := m.store.UpdateSet(id, json.RawMessage(body))
	if err != nil {
		if strings.Contains(err.Error(), "занят") {
			return conflict(err.Error())
		}
		return err
	}
	if running {
		if _, err := m.RestartSet(context.Background(), set); err != nil {
			m.log.Warn("перезапуск набора после правки не удался", "set", id, "err", err)
		}
	}
	writeJSON(req.w, http.StatusOK, m.setView(set))
	return nil
}

func (m *Manager) apiSetDelete(req *request) error {
	id := req.args[0]
	set := m.store.Set(id)
	if set == nil {
		return notFound("нет набора " + id)
	}
	m.rt.Stop(set)
	m.health.clear(id)
	if err := m.store.DeleteSet(id); err != nil {
		return err
	}
	writeJSON(req.w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (m *Manager) startSet(ctx context.Context, set *registry.Set) (RuntimeStatus, error) {
	status, err := m.rt.Start(ctx, set, m.store.Servers())
	if err != nil {
		if strings.Contains(err.Error(), "нет активного ключа") {
			return status, conflict(err.Error())
		}
		return status, serverError(err.Error())
	}
	m.health.clear(set.ID)
	return status, nil
}

func (m *Manager) RestartSet(ctx context.Context, set *registry.Set) (RuntimeStatus, error) {
	status, err := m.rt.Restart(ctx, set, m.store.Servers())
	if err != nil {
		if strings.Contains(err.Error(), "нет активного ключа") {
			return status, conflict(err.Error())
		}
		return status, serverError(err.Error())
	}
	m.health.clear(set.ID)
	return status, nil
}

func (m *Manager) apiSetAction(req *request) error {
	id, action := req.args[0], req.args[1]
	set := m.store.Set(id)
	if set == nil {
		return notFound("нет набора " + id)
	}
	switch action {
	case "start":
		status, err := m.startSet(req.r.Context(), set)
		if err != nil {
			return err
		}
		writeJSON(req.w, http.StatusOK, status)
	case "stop":
		status := m.rt.Stop(set)
		m.health.clear(id)
		writeJSON(req.w, http.StatusOK, status)
	case "restart":
		status, err := m.RestartSet(req.r.Context(), set)
		if err != nil {
			return err
		}
		writeJSON(req.w, http.StatusOK, status)
	}
	return nil
}

func (m *Manager) apiSetLogs(req *request) error {
	id := req.args[0]
	if m.store.Set(id) == nil {
		return notFound("нет набора " + id)
	}
	writeJSON(req.w, http.StatusOK, map[string]any{
		"logs": m.rt.Logs(id, linesParam(req.r, 200)),
	})
	return nil
}

func (m *Manager) apiServerLogs(req *request) error {
	id, name := req.args[0], req.args[1]
	set := m.store.Set(id)
	if set == nil {
		return notFound("нет набора " + id)
	}
	if !containsName(set.Servers, name) {
		return notFound("нет сервера " + name + " в наборе " + id)
	}
	writeJSON(req.w, http.StatusOK, map[string]any{
		"server": name,
		"logs":   m.rt.ServerLogs(id, name, linesParam(req.r, 200)),
	})
	return nil
}

func (m *Manager) apiServerRestart(req *request) error {
	id, name := req.args[0], req.args[1]
	set := m.store.Set(id)
	if set == nil {
		return notFound("нет набора " + id)
	}
	if !containsName(set.Servers, name) {
		return notFound("нет сервера " + name + " в наборе " + id)
	}
	if _, err := m.RestartSet(req.r.Context(), set); err != nil {
		return err
	}
	writeJSON(req.w, http.StatusOK, map[string]any{"restarted": true, "scope": "set"})
	return nil
}

func (m *Manager) apiSetServers(req *request) error {
	id := req.args[0]
	set := m.store.Set(id)
	if set == nil {
		return notFound("нет набора " + id)
	}
	names := set.Servers
	status := m.rt.status(set)
	if !status.Running {
		servers := make([]serverHealth, 0, len(names))
		for _, name := range names {
			servers = append(servers, serverHealth{Name: name, Error: "набор не запущен"})
		}
		writeJSON(req.w, http.StatusOK, setHealth{Running: false, Servers: servers})
		return nil
	}
	view := m.health.get(req.r.Context(), id, func(ctx context.Context) setHealth {
		return fetchSetHealth(ctx, m.rt.probeAddr, set.Port, set.Token, set.Open, names, m.version)
	})
	view.Running = status.Running
	writeJSON(req.w, http.StatusOK, view)
	return nil
}

// ─── keys ────────────────────────────────────────────────────────────────────

func (m *Manager) apiKeyIssue(req *request) error {
	id := req.args[0]
	set := m.store.Set(id)
	if set == nil {
		return notFound("нет набора " + id)
	}
	running := m.rt.status(set).Running
	token, err := m.store.IssueKey(id)
	if err != nil {
		return err
	}
	updated := m.store.Set(id)
	if running {
		if _, err := m.RestartSet(context.Background(), updated); err != nil {
			m.log.Warn("перезапуск набора после выдачи ключа не удался", "set", id, "err", err)
		}
	}
	writeJSON(req.w, http.StatusOK, map[string]any{
		"token": token,
		"key":   activeKey(updated),
		"set":   m.setView(updated),
	})
	return nil
}

func (m *Manager) apiKeyRevoke(req *request) error {
	id, keyID := req.args[0], req.args[1]
	cur := m.store.Set(id)
	if cur == nil {
		return notFound("нет набора " + id)
	}
	known := false
	for _, key := range registry.PublicSet(cur).Keys {
		if key.ID == keyID {
			known = true
			break
		}
	}
	if !known {
		return notFound("нет ключа " + keyID)
	}
	wasActive, err := m.store.RevokeKey(id, keyID)
	if err != nil {
		return err
	}
	set := m.store.Set(id)
	if wasActive {
		m.rt.Stop(set)
		m.health.clear(id)
	}
	var revoked registry.PublicKey
	for _, key := range registry.PublicSet(set).Keys {
		if key.ID == keyID {
			revoked = key
		}
	}
	writeJSON(req.w, http.StatusOK, map[string]any{
		"ok":        true,
		"wasActive": wasActive,
		"key":       revoked,
		"set":       m.setView(set),
	})
	return nil
}

func activeKey(set *registry.Set) any {
	if set == nil {
		return nil
	}
	for _, key := range registry.PublicSet(set).Keys {
		if key.RevokedAt == "" {
			return key
		}
	}
	return nil
}

// ─── вспомогательные ────────────────────────────────────────────────────────

// restartSetsUsingEnv перезапускает работающие наборы, в серверах которых
// встречается ${name}: остальных правка секрета не касается.
func (m *Manager) restartSetsUsingEnv(name string) []string {
	out := []string{}
	servers := m.store.Servers()
	for _, set := range m.store.Sets() {
		if !containsName(envRefs(setServers(set, servers)), name) || !m.rt.status(set).Running {
			continue
		}
		if _, err := m.RestartSet(context.Background(), set); err != nil {
			m.log.Warn("перезапуск набора после смены секрета не удался", "set", set.ID, "err", err)
			continue
		}
		out = append(out, set.ID)
	}
	return out
}

func (m *Manager) restartSetsUsing(server string) []string {
	out := []string{}
	for _, set := range m.store.Sets() {
		if !containsName(set.Servers, server) || !m.rt.status(set).Running {
			continue
		}
		if _, err := m.RestartSet(context.Background(), set); err != nil {
			m.log.Warn("перезапуск набора после правки сервера не удался", "set", set.ID, "err", err)
			continue
		}
		out = append(out, set.ID)
	}
	return out
}

func (m *Manager) stopSetsUsing(server string) []string {
	out := []string{}
	for _, set := range m.store.Sets() {
		if !containsName(set.Servers, server) {
			continue
		}
		// Статус — до Stop: Stop удаляет запись, и после него набор всегда «не работает».
		wasRunning := m.rt.status(set).Running
		m.rt.Stop(set)
		if wasRunning {
			out = append(out, set.ID)
		}
		m.health.clear(set.ID)
	}
	return out
}

func containsName(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

func linesParam(r *http.Request, def int) int {
	v := r.URL.Query().Get("lines")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func serversOrEmpty(servers []registry.Server) []registry.Server {
	if servers == nil {
		return []registry.Server{}
	}
	return servers
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
