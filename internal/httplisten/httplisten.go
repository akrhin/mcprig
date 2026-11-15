// Package httplisten exposes one set as a stateless MCP HTTP endpoint:
// bearer auth, Host/Origin checks and a body cap in front of the go-sdk
// Streamable HTTP handler.
package httplisten

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
	"github.com/akrhin/mcprig/internal/toolout"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxBodyBytes is the contract limit for one proxied MCP request.
const maxBodyBytes = 4 << 20

// New builds the set's HTTP handler. A missing authToken is a startup error
// unless the set is open (AllowAnonymous): the proxy never listens without
// authentication by accident.
func New(cfg *config.Config, srv *mcp.Server, log *slog.Logger, rec *stats.Recorder) (http.Handler, error) {
	if cfg == nil || cfg.Transport == nil {
		return nil, errors.New("httplisten: у набора нет transport — задайте stdio или http")
	}
	if srv == nil {
		return nil, errors.New("httplisten: у набора нет серверов")
	}
	token := cfg.Transport.AuthToken
	allowAnonymous := cfg.Transport.AllowAnonymous
	if token == "" && !allowAnonymous {
		return nil, errors.New("httplisten: пустой authToken — выпустите ключ (без авторизации набор не стартует)")
	}
	if log == nil {
		log = slog.Default()
	}
	allowed := cfg.Transport.AllowedHosts
	if len(allowed) == 0 {
		// The SDK's own DNS-rebinding guard is intentionally off, so this list
		// is the only Host policy: an empty config must not mean "any host".
		allowed = []string{"127.0.0.1", "localhost", "::1"}
		log.Warn("httplisten: allowedHosts пуст — принимаем только loopback", "hosts", allowed)
	} else {
		for _, host := range allowed {
			if host == "*" {
				log.Warn("httplisten: allowedHosts содержит \"*\" — защита от rebinding отключена для любого Host")
			}
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
		log.Warn("httplisten: tool-out", "warn", w)
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
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle(path, guard(mcpHandler, token, allowAnonymous, allowed, cfg.Transport.ClientNames))
	// Усечённый ответ лежит файлом рядом с этим процессом; клиент забирает его
	// тем же путём, что и /mcp. Послабления открытому набору нет: файл не
	// отдаётся анонимно никогда, даже когда /mcp пускает без ключа.
	mux.Handle("GET /tool-out/{name}", guard(toolOutHandler(toolout.Dir(toolout.Base()), retention.TTL), token, false, allowed, cfg.Transport.ClientNames))
	if rec != nil {
		// The panel polls this endpoint; it carries the same bearer as /mcp.
		// У открытого набора bearer нет ни у кого, поэтому анонимный снимок
		// редактируется: без меток клиентов и текстов ошибок апстрима.
		mux.Handle("GET /stats", guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Редакция выбирается до снимка: собранная в редакции копия не
			// платит за полный снимок, который тут же был бы выброшен.
			anon := token == "" || !bearerOK(r, token)
			var snap stats.Snapshot
			if anon {
				snap = rec.SnapshotRedacted()
			} else {
				snap = rec.Snapshot()
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(snap)
		}), token, allowAnonymous, allowed, cfg.Transport.ClientNames))
	}
	return logRequests(mux, log), nil
}

// toolOutName is the shape createToolOut gives a dump. Anything else is 404,
// not 400: a wrong name must not confirm that a tool-out directory exists.
var toolOutName = regexp.MustCompile(`^[0-9a-f]{16}$`)

// toolOutHandler serves one saved answer from this process's tool-out. A name
// past its TTL is 410: the file was there and the marker's promise ran out.
//
// shortcut: имя выгрузки — 64-битный capability без привязки к клиенту: любой
// держатель ключа набора заберёт чужую выгрузку, если знает имя
// (docs/history/proposals/0002-proxy-improvements.md).
// shortcut: TTL конечной точки не прокидывается в набор под панелью, прокинуть
// вместе с профилями/лимитами (T2/T3).
func toolOutHandler(dir string, ttl time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !toolOutName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(dir, name+".txt"))
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

// guard is the per-request policy in front of the MCP handler: method, Host,
// Origin, bearer token, body cap.
func guard(next http.Handler, token string, allowAnonymous bool, allowed []string, names map[string]string) http.Handler {
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

		host := hostOnly(r.Host)
		if !hostAllowed(host, allowed) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !hostAllowed(hostOnly(u.Host), allowed) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}

		// No header at all is allowed only for an open set; a token that is
		// presented must always be the right one.
		// Открытый набор: предъявленный заголовок сравнивается с пустым токеном
		// и всегда получает 401 — клиент, который шлёт Authorization всегда,
		// в открытый набор не пройдёт (нужен ключ либо молчание).
		if r.Header.Get("Authorization") == "" {
			if !allowAnonymous {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		} else if !bearerOK(r, token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Отпечаток ключа едет отдельно от метки: подпись ключа и _meta
		// clientInfo — не идентичность, а лимит частоты корзинит по ключу.
		if presented, ok := bearerToken(r); ok {
			r = r.WithContext(stats.WithClientKey(r.Context(), stats.Fingerprint(presented)))
		}
		// Адрес — то, чем опознаётся клиент открытого набора: ключа у него нет,
		// а метка самообъявлена. Порт срезан, иначе одна машина = много корзин.
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			r = r.WithContext(stats.WithClientAddr(r.Context(), host))
		}
		r = r.WithContext(stats.WithClient(r.Context(), clientLabel(r, names)))

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

func bearerOK(r *http.Request, token string) bool {
	presented, ok := bearerToken(r)
	if !ok {
		return false
	}
	// ConstantTimeCompare returns 0 on a length mismatch, so this leaks only
	// the token length.
	return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}

// hostOnly drops the port: AllowedHosts is a host list, not a listener list.
func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

func hostAllowed(host string, allowed []string) bool {
	for _, a := range allowed {
		if a == "*" || strings.EqualFold(hostOnly(a), host) {
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
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start),
			"remote", r.RemoteAddr,
		)
	})
}
