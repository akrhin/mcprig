// Package httplisten exposes one set as a stateless MCP HTTP endpoint:
// bearer auth, Host/Origin checks and a body cap in front of the go-sdk
// Streamable HTTP handler.
package httplisten

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/safelog"
	"github.com/akrhin/mcprig/internal/stats"
	"github.com/akrhin/mcprig/internal/toolout"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxBodyBytes is the contract limit for one proxied MCP request.
const maxBodyBytes = 4 << 20

// Server is the set's HTTP handler. The access policy can be swapped while the
// process runs: /api/sets/<id>/keys delivers a new key, and losing the last key
// of an open set opens it again, over reload, not over a restart of the set.
type Server struct {
	http.Handler
	auth *authSet
}

// SetAuth replaces the accepted tokens, the set's openness and the key labels
// in one step: a newly issued key starts working, a revoked one stops, and an
// open set that gained its first key stops admitting a caller without a header.
func (s *Server) SetAuth(tokens []string, allowAnonymous bool, names map[string]string) {
	s.auth.set(tokens, allowAnonymous, names)
}

// authSet is the guard's live state. One swap per reload, and each request
// reads the state current at its start: a request never sees half a policy.
type authSet struct {
	tokens    *tokenSet
	anonymous atomic.Bool
	names     atomic.Pointer[map[string]string]
}

func newAuthSet(tokens []string, allowAnonymous bool, names map[string]string) *authSet {
	a := &authSet{tokens: &tokenSet{}}
	a.set(tokens, allowAnonymous, names)
	return a
}

func (a *authSet) set(tokens []string, allowAnonymous bool, names map[string]string) {
	a.tokens.set(tokens)
	a.anonymous.Store(allowAnonymous)
	if names == nil {
		names = map[string]string{}
	}
	a.names.Store(&names)
}

// tokenSet хранит хэши токенов: сравнение идёт с хэшем предъявленного, поэтому
// длина токена не утекает, а сравнение с каждым элементом — постоянное по
// времени. Пустой набор принимает только открытый набор.
type tokenSet struct {
	v atomic.Pointer[[][sha256.Size]byte]
}

func (ts *tokenSet) set(tokens []string) {
	hashes := make([][sha256.Size]byte, 0, len(tokens))
	for _, token := range tokens {
		if token = strings.TrimSpace(token); token != "" {
			hashes = append(hashes, sha256.Sum256([]byte(token)))
		}
	}
	ts.v.Store(&hashes)
}

// ok reports whether presented is one of the accepted tokens.
func (ts *tokenSet) ok(presented string) bool {
	hashes := ts.v.Load()
	if hashes == nil || len(*hashes) == 0 {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	match := 0
	for _, want := range *hashes {
		match |= subtle.ConstantTimeCompare(got[:], want[:])
	}
	return match == 1
}

// New builds the set's HTTP handler. A missing token is a startup error unless
// the set is open (AllowAnonymous): the proxy never listens without
// authentication by accident. The token list is transport.authTokens; a file
// with the single authToken still works.
func New(cfg *config.Config, srv *mcp.Server, log *slog.Logger, rec *stats.Recorder) (*Server, error) {
	if cfg == nil || cfg.Transport == nil {
		return nil, errors.New("httplisten: у набора нет transport — задайте stdio или http")
	}
	if srv == nil {
		return nil, errors.New("httplisten: у набора нет серверов")
	}
	tokens := cfg.Transport.AuthTokens
	if len(tokens) == 0 && cfg.Transport.AuthToken != "" {
		tokens = []string{cfg.Transport.AuthToken}
	}
	allowAnonymous := cfg.Transport.AllowAnonymous
	if len(tokens) == 0 && !allowAnonymous {
		return nil, errors.New("httplisten: пустой authToken — выпустите ключ (без авторизации набор не стартует)")
	}
	if log == nil {
		log = slog.Default()
	}
	// The SDK's own DNS-rebinding guard is intentionally off, so this list is
	// the only Host policy: an empty config must not mean "any host". The
	// guard passes it to HostAllowed, which owns the loopback default.
	allowed := cfg.Transport.AllowedHosts
	if len(allowed) == 0 {
		log.Warn("httplisten: allowedHosts пуст — принимаем только loopback", "hosts", loopbackHosts)
	}
	for _, host := range allowed {
		if host == "*" {
			log.Warn("httplisten: allowedHosts содержит \"*\" — защита от rebinding отключена для любого Host")
		}
	}
	path := cfg.Transport.Path
	if path == "" {
		path = "/mcp"
	}
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("httplisten: path должен начинаться с «/»: получено " + path)
	}

	// Срок жизни выгрузки читается один раз: маркер обещает файл, и отдавать
	// его дольше этого срока нельзя, даже если чистка до файла не дошла.
	retention, warns := toolout.FromEnv()
	for _, w := range warns {
		log.Warn("httplisten: tool-out", "warn", safelog.Value(w))
	}
	// Набор под панелью получает срок из реестра (BuildSetConfig кладёт его в
	// servers.json): окружение набора собирается вручную, и MCPRIG_TOOLOUT_TTL
	// до него не доезжает. Ручной serve по-прежнему живёт на MCPRIG_TOOLOUT_TTL.
	if v := strings.TrimSpace(cfg.Transport.ToolOutTTL); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			retention.TTL = d
		} else {
			log.Warn("httplisten: tool-out", "warn",
				fmt.Sprintf("transport.toolOutTtl=%q: срок должен быть положительной длительностью — беру %s", v, retention.TTL))
		}
	}

	// Stateless 2026-07-28 per SEP-2567: one temporary session per POST. The
	// same handler still serves 2025-era protocol versions (the SDK
	// synthesizes their initialization state), so old clients work unchanged.
	// SDK-localhost DNS-rebinding protection is off: AllowedHosts below is the
	// one Host policy, and it can admit a configured non-loopback name.
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			Stateless:                  true,
			Logger:                     log,
			MaxRequestBodyBytes:        maxBodyBytes,
			DisableLocalhostProtection: true,
		},
	)

	mux := http.NewServeMux()
	auth := newAuthSet(tokens, allowAnonymous, cfg.Transport.ClientNames)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle(path, guard(mcpHandler, auth, true, allowed))
	// Усечённый ответ лежит файлом рядом с этим процессом; клиент забирает его
	// тем же путём, что и /mcp. Послабления открытому набору нет: файл не
	// отдаётся анонимно никогда, даже когда /mcp пускает без ключа.
	mux.Handle("GET /tool-out/{name}", guard(toolOutHandler(toolout.Dir(toolout.Base()), retention.TTL), auth, false, allowed))
	if rec != nil {
		// The panel polls this endpoint; it carries the same bearer as /mcp.
		// У открытого набора bearer нет ни у кого, поэтому анонимный снимок
		// редактируется: без меток клиентов и текстов ошибок апстрима.
		mux.Handle("GET /stats", guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Редакция выбирается до снимка: собранная в редакции копия не
			// платит за полный снимок, который тут же был бы выброшен.
			anon := !bearerOK(r, auth.tokens)
			var snap stats.Snapshot
			if anon {
				snap = rec.SnapshotRedacted()
			} else {
				snap = rec.Snapshot()
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(snap)
		}), auth, true, allowed))
	}
	return &Server{Handler: logRequests(mux, log), auth: auth}, nil
}

// toolOutName is the shape createToolOut gives a dump. Anything else is 404,
// not 400: a wrong name must not confirm that a tool-out directory exists.
var toolOutName = regexp.MustCompile(`^[0-9a-f]{16}$`)

// toolOutHandler serves one saved answer from this process's tool-out. A name
// past its TTL is 410: the file was there and the marker's promise ran out.
//
// The dump belongs to the key whose call produced it: <name>.owner holds that
// key's fingerprint, and a reader with another key — or with no .owner beside
// the file — gets 404, not 403, because a wrong name must not confirm that a
// dump exists. A caller without a key (an open set) owns the dump as
// toolout.AnonymousOwner and reads it back under the same name.
//
// shortcut: имя по-прежнему capability внутри одного клиента — знающий имя
// читает свою выгрузку, чужой ключ получает 404, а отозванный ключ теряет
// доступ к файлу, который живёт до TTL
// (docs/history/proposals/0002-proxy-improvements.md).
func toolOutHandler(dir string, ttl time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !toolOutName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(dir, name+".txt")
		owner, err := os.ReadFile(toolout.OwnerPath(path))
		if err != nil || strings.TrimSpace(string(owner)) != readerOwner(r) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if time.Since(info.ModTime()) > ttl {
			http.Error(w, "полный ответ больше не хранится", http.StatusGone)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		// Имя ведёт на выгрузку внутри эндпоинта набора: реферер наружу не нужен.
		w.Header().Set("Referrer-Policy", "no-referrer")
		// ServeContent режет Range и ставит Last-Modified по mtime.
		http.ServeContent(w, r, "", info.ModTime(), f)
	}
}

// readerOwner is the fingerprint the guard recorded for this request. A request
// without a key matches the dump an open set wrote without one.
func readerOwner(r *http.Request) string {
	if fp := stats.ClientKey(r.Context()); fp != "" {
		return fp
	}
	return toolout.AnonymousOwner
}

// guard is the per-request policy in front of the MCP handler: method, Host,
// Origin, bearer token, body cap. openRoute marks the routes an open set also
// serves without a header; /tool-out is not one of them.
func guard(next http.Handler, auth *authSet, openRoute bool, allowed []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		// HEAD бесплатен и у /mcp (тело не отдаётся), а /tool-out по нему
		// говорит длину выгрузки, не скачивая её.
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodDelete:
		default:
			w.Header().Set("Allow", "GET, HEAD, POST, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if !HostAllowed(r.Host, allowed) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !HostAllowed(u.Host, allowed) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}

		// No header at all is allowed only for an open set; a token that is
		// presented must always be the right one.
		// Открытость читается на каждом запросе: ключ, выданный или отозванный
		// под панелью, меняет её без перезапуска набора.
		// Открытый набор: предъявленный заголовок сравнивается с пустым токеном
		// и всегда получает 401 — клиент, который шлёт Authorization всегда,
		// в открытый набор не пройдёт (нужен ключ либо молчание).
		if r.Header.Get("Authorization") == "" {
			if !openRoute || !auth.anonymous.Load() {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		} else if !bearerOK(r, auth.tokens) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Отпечаток ключа едет отдельно от метки: подпись ключа и _meta
		// clientInfo — не идентичность, а лимит частоты корзинит по ключу.
		if presented, ok := bearerToken(r); ok {
			r = r.WithContext(stats.WithClientKey(r.Context(), stats.Fingerprint(presented)))
		}
		// Служебный вызов панели (проба здоровья, каталог) — не клиентский
		// трафик: он не попадает ни в счётчики, ни в хвост, ни в историю.
		if r.Header.Get(stats.ServiceCallHeader) == stats.ServiceCallerPanel {
			r = r.WithContext(stats.WithServiceCall(r.Context()))
		}
		// Адрес — то, чем опознаётся клиент открытого набора: ключа у него нет,
		// а метка самообъявлена. Порт срезан, иначе одна машина = много корзин.
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			r = r.WithContext(stats.WithClientAddr(r.Context(), host))
		}
		r = r.WithContext(stats.WithClient(r.Context(), clientLabel(r, *auth.names.Load())))

		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// clientLabel names the caller for the counters: the key's note when the owner
// has named the key, otherwise the User-Agent product token plus a short
// fingerprint of the presented key, so two harnesses on one key stay apart
// without the key itself ever reaching the stats.
func clientLabel(r *http.Request, names map[string]string) string {
	ua := ""
	if fields := strings.Fields(r.UserAgent()); len(fields) > 0 {
		ua = fields[0]
	}
	key := ""
	// Отпечаток считается только по реально предъявленному bearer-ключу:
	// пустой заголовок, "Bearer " без значения или чужую схему нельзя
	// превращать в «валидный» отпечаток пустой строки.
	if presented, ok := bearerToken(r); ok {
		key = stats.Fingerprint(presented)
	}
	// Подпись ключа важнее отпечатка: владелец уже назвал клиента, и метка
	// с User-Agent в этом случае только мешает.
	if name := names[key]; name != "" {
		return name
	}
	// Длину и управляющие символы метки режет stats.Record: это единственная
	// точка, где сходятся HTTP-метка и clientInfo из initialize.
	return stats.Label(key, ua)
}

// bearerToken — значение Bearer-заголовка: схема без учёта регистра, пробелы
// вокруг значения срезаются. Метка клиента и проверка токена обязаны
// понимать заголовок одинаково, иначе "Bearer  tok" даёт метку и 401.
func bearerToken(r *http.Request) (string, bool) {
	const scheme = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(scheme):])
	return tok, tok != ""
}

func bearerOK(r *http.Request, tokens *tokenSet) bool {
	presented, ok := bearerToken(r)
	if !ok {
		return false
	}
	return tokens.ok(presented)
}

// loopbackHosts is the Host policy when none is configured: one copy for the
// set endpoint and the panel, so "not set" never comes to mean "any host".
var loopbackHosts = []string{"127.0.0.1", "localhost", "::1"}

// HostOnly drops the port: AllowedHosts is a host list, not a listener list.
func HostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

// HostAllowed is the one Host/rebinding policy for a set endpoint and for the
// panel. An empty list means loopback; an entry may carry a port, ignored like
// the checked host's.
func HostAllowed(hostport string, allowed []string) bool {
	if len(allowed) == 0 {
		allowed = loopbackHosts
	}
	host := HostOnly(hostport)
	for _, a := range allowed {
		if a == "*" || strings.EqualFold(HostOnly(a), host) {
			return true
		}
	}
	return false
}

// statusWriter records the status for the access log. Unwrap keeps SSE
// flushing (http.ResponseController) working through the wrapper.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequests(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		// Headers, body and the Authorization value never reach this line.
		log.Info("http",
			"method", safelog.Value(r.Method),
			"path", safelog.Value(r.URL.Path),
			"status", sw.status,
			"duration", time.Since(start),
			"remote", safelog.Value(r.RemoteAddr),
		)
	})
}
