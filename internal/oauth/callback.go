package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultCallbackAddr — адрес loopback-приёмника по умолчанию.
const DefaultCallbackAddr = "127.0.0.1:8947"

// callbackTimeout — сколько ждём редирект от браузера.
const callbackTimeout = 5 * time.Minute

// Result — параметры успешного редиректа авторизации.
type Result struct {
	Code  string
	State string
	Iss   string
}

// Callback — одноразовый HTTP-приёмник редиректа на loopback-адресе.
type Callback struct {
	srv    *http.Server
	url    string
	done   chan struct{}
	res    chan *Result
	errs   chan error
	closed sync.Once

	mu       sync.Mutex
	waiting  bool
	expected string
}

// NewCallback поднимает приёмник на addr ("127.0.0.1:8947"). Пустой addr —
// DefaultCallbackAddr; пустой порт — эфемерный, фактический виден в URL().
// state — ожидаемое значение из URL авторизации: пустой отвергается, потому
// что иначе приёмник принимал бы любой редирект. Слушается только loopback.
func NewCallback(addr, state string) (*Callback, error) {
	if state == "" {
		return nil, errors.New("oauth: не задан ожидаемый state для приёмника кода")
	}
	if addr == "" {
		addr = DefaultCallbackAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("oauth: неверный адрес приёмника кода %q: %w", addr, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("oauth: приёмник кода слушает только loopback, получен %q", host)
	}
	if port == "" {
		port = "0"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("oauth: занять порт %s для приёма кода: %w (занят? смените callbackPort в настройках)", addr, err)
	}

	c := &Callback{
		done:     make(chan struct{}),
		res:      make(chan *Result, 1),
		errs:     make(chan error, 1),
		expected: state,
	}
	_, actualPort, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("oauth: определить порт приёмника: %w", err)
	}
	c.url = "http://" + net.JoinHostPort(host, actualPort) + "/callback"

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", c.handleCallback)
	c.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go c.srv.Serve(ln)
	return c, nil
}

// URL — адрес редиректа, который должен совпадать с RedirectURL обработчика.
func (c *Callback) URL() string { return c.url }

// Wait ждёт редирект. Одновременно допускается один ожидающий.
func (c *Callback) Wait(ctx context.Context) (*Result, error) {
	c.mu.Lock()
	if c.waiting {
		c.mu.Unlock()
		return nil, errors.New("another authorization is already in progress")
	}
	c.waiting = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.waiting = false
		c.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, callbackTimeout)
	defer cancel()
	select {
	case r := <-c.res:
		return r, nil
	case err := <-c.errs:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("ожидание OAuth-кода прервано: %w", ctx.Err())
	case <-c.done:
		return nil, errors.New("приём OAuth-кода закрыт")
	}
}

// Close останавливает приёмник; ожидающий Wait снимается.
func (c *Callback) Close() error {
	c.closed.Do(func() { close(c.done) })
	return c.srv.Close()
}

func (c *Callback) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// state сверяется первым: чужой редирект — в том числе с ?error= — не
	// должен снимать ожидание этого приёмника, иначе любой прохожий гасит
	// вход, открытый в браузере владельца.
	state := q.Get("state")
	if state != c.expected {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}

	if e := q.Get("error"); e != "" {
		msg := e
		if d := q.Get("error_description"); d != "" {
			msg = e + ": " + d
		}
		http.Error(w, "authorization failed: "+msg, http.StatusBadRequest)
		c.fail(fmt.Errorf("сервер авторизации вернул ошибку: %s", msg))
		return
	}

	code := q.Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		c.fail(errors.New("редирект без параметра code"))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, successPage)
	c.succeed(&Result{Code: code, State: state, Iss: q.Get("iss")})
}

func (c *Callback) succeed(r *Result) {
	select {
	case c.res <- r:
	default:
	}
}

func (c *Callback) fail(err error) {
	select {
	case c.errs <- err:
	default:
	}
}

func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "::1":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

const successPage = `<!doctype html>
<html lang="ru">
<head><meta charset="utf-8"><title>Авторизация завершена</title></head>
<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#06171C;color:#E6EFF1;font:16px/1.5 system-ui,sans-serif">
<main style="text-align:center;padding:24px">
<h1 style="font-size:20px;margin:0 0 8px">Авторизация завершена</h1>
<p style="margin:0">Вкладку можно закрыть.</p>
</main>
</body>
</html>
`
