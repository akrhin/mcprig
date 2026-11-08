// Package httplisten exposes one set as a stateless MCP HTTP endpoint:
// bearer auth, Host/Origin checks and a body cap in front of the go-sdk
// Streamable HTTP handler.
package httplisten

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sintez/mcp-manager/internal/config"
)

// maxBodyBytes is the contract limit for one proxied MCP request.
const maxBodyBytes = 4 << 20

// New builds the set's HTTP handler. An empty AuthToken is a startup error:
// the proxy never listens without authentication.
func New(cfg *config.Config, srv *mcp.Server, log *slog.Logger) (http.Handler, error) {
	if cfg == nil || cfg.Transport == nil {
		return nil, errors.New("httplisten: в конфиге нет transport")
	}
	if srv == nil {
		return nil, errors.New("httplisten: нет mcp-сервера")
	}
	token := cfg.Transport.AuthToken
	if token == "" {
		return nil, errors.New("httplisten: пустой authToken — без авторизации набор не стартует")
	}
	if log == nil {
		log = slog.Default()
	}
	allowed := cfg.Transport.AllowedHosts
	if len(allowed) == 0 {
		log.Warn("httplisten: allowedHosts пуст — принимаем любой Host и Origin")
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
	mux.Handle(path, guard(mcpHandler, token, allowed))
	return logRequests(mux, log), nil
}

// guard is the per-request policy in front of the MCP handler: method, Host,
// Origin, bearer token, body cap.
func guard(next http.Handler, token string, allowed []string) http.Handler {
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

		if !bearerOK(r, token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
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
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if strings.EqualFold(hostOnly(a), host) {
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
