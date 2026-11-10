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
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/stats"
)

// maxBodyBytes is the contract limit for one proxied MCP request.
const maxBodyBytes = 4 << 20

// New builds the set's HTTP handler. A missing authToken is a startup error
// unless the set is open (AllowAnonymous): the proxy never listens without
// authentication by accident.
func New(cfg *config.Config, srv *mcp.Server, log *slog.Logger, rec *stats.Recorder) (http.Handler, error) {
	if cfg == nil || cfg.Transport == nil {
		return nil, errors.New("httplisten: в конфиге нет transport")
	}
	if srv == nil {
		return nil, errors.New("httplisten: нет mcp-сервера")
	}
	token := cfg.Transport.AuthToken
	allowAnonymous := cfg.Transport.AllowAnonymous
	if token == "" && !allowAnonymous {
		return nil, errors.New("httplisten: пустой authToken — без авторизации набор не стартует")
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
		return nil, errors.New("httplisten: path должен начинаться с /")
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

// guard is the per-request policy in front of the MCP handler: method, Host,
// Origin, bearer token, body cap.
func guard(next http.Handler, token string, allowAnonymous bool, allowed []string, names map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodPost, http.MethodDelete:
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
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
	const scheme = "bearer "
	// Отпечаток считается только по реально предъявленному bearer-ключу:
	// пустой заголовок, "Bearer " без значения или чужую схему нельзя
	// превращать в «валидный» отпечаток пустой строки.
	if h := r.Header.Get("Authorization"); len(h) > len(scheme) && strings.EqualFold(h[:len(scheme)], scheme) {
		if presented := strings.TrimSpace(h[len(scheme):]); presented != "" {
			key = stats.Fingerprint(presented)
		}
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

func bearerOK(r *http.Request, token string) bool {
	const scheme = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return false
	}
	// ConstantTimeCompare returns 0 on a length mismatch, so this leaks only
	// the token length.
	return subtle.ConstantTimeCompare([]byte(h[len(scheme):]), []byte(token)) == 1
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
