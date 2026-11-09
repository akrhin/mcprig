package manager

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "mcp_session"
	sessionTTL    = 12 * time.Hour

	modeNone          = "none"
	modeProxy         = "proxy"
	modePassword      = "password"
	modeProxyPassword = "proxy+password"
)

// AuthOptions — вход в API. Пароли сравниваются за постоянное время.
type AuthOptions struct {
	Password     string
	PasswordHash string
	ProxySecret  string
	User         string
	UserHeader   string
	AllowedHosts []string
	Off          bool
}

type scryptHash struct {
	n, r, p int
	salt    []byte
	key     []byte
}

func parseScryptHash(s string) (*scryptHash, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "scrypt" {
		return nil, errors.New("MCPRIG_PASSWORD_HASH: ожидался scrypt$N$r$p$salt$hash")
	}
	n, err1 := strconv.Atoi(parts[1])
	r, err2 := strconv.Atoi(parts[2])
	p, err3 := strconv.Atoi(parts[3])
	salt, err4 := hex.DecodeString(parts[4])
	key, err5 := hex.DecodeString(parts[5])
	if err := errors.Join(err1, err2, err3, err4, err5); err != nil {
		return nil, fmt.Errorf("MCPRIG_PASSWORD_HASH: %w", err)
	}
	if n < 2 || r < 1 || p < 1 || len(salt) == 0 || len(key) == 0 {
		return nil, errors.New("MCPRIG_PASSWORD_HASH: пустые параметры")
	}
	return &scryptHash{n: n, r: r, p: p, salt: salt, key: key}, nil
}

func (h *scryptHash) verify(password string) bool {
	key, err := scryptKey([]byte(password), h.salt, h.n, h.r, h.p, len(h.key))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, h.key) == 1
}

// hashPassword — формат scrypt$N$r$p$соль$хеш, как в контракте.
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := scryptKey([]byte(password), salt, 16384, 8, 1, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt$16384$8$1$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

type session struct {
	user    string
	expires time.Time
}

// sessionStore держит сессии в памяти: рестарт менеджера их разлогинивает.
type sessionStore struct {
	mu     sync.Mutex
	secret []byte
	items  map[string]session
}

func newSessionStore() (*sessionStore, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return &sessionStore{secret: secret, items: map[string]session{}}, nil
}

func (s *sessionStore) create(user string) string {
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		panic(err) // crypto/rand на Linux не отказывает
	}
	v := hex.EncodeToString(token)
	s.mu.Lock()
	s.items[v] = session{user: user, expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	return v + "." + s.sign(v)
}

func (s *sessionStore) sign(v string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *sessionStore) get(cookieValue string) (session, bool) {
	v, sig, ok := strings.Cut(cookieValue, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(v))) {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.items[v]
	if !ok {
		return session{}, false
	}
	if time.Now().After(sess.expires) {
		delete(s.items, v)
		return session{}, false
	}
	return sess, true
}

func (s *sessionStore) destroy(cookieValue string) {
	if v, _, ok := strings.Cut(cookieValue, "."); ok {
		s.mu.Lock()
		delete(s.items, v)
		s.mu.Unlock()
	}
}

func (s *sessionStore) gc() {
	now := time.Now()
	s.mu.Lock()
	for k, v := range s.items {
		if now.After(v.expires) {
			delete(s.items, k)
		}
	}
	s.mu.Unlock()
}

type auth struct {
	mode         string
	password     string
	hash         *scryptHash
	proxySecret  string
	user         string
	userHeader   string
	allowedHosts []string
	sessions     *sessionStore

	// Защита /api/login: счётчик неудач по IP и один scrypt за раз.
	loginMu    sync.Mutex
	loginFails map[string]*failWindow
	loginSem   chan struct{}
}

type failWindow struct {
	count int
	reset time.Time
}

const (
	loginMaxFails   = 5
	loginFailWindow = time.Minute
)

func newAuth(o AuthOptions) (*auth, error) {
	a := &auth{
		password:     o.Password,
		proxySecret:  o.ProxySecret,
		user:         o.User,
		userHeader:   o.UserHeader,
		allowedHosts: o.AllowedHosts,
	}
	if a.user == "" {
		a.user = "admin"
	}
	if a.userHeader == "" {
		a.userHeader = "Remote-User"
	}
	if o.PasswordHash != "" {
		h, err := parseScryptHash(o.PasswordHash)
		if err != nil {
			return nil, err
		}
		a.hash = h
		a.password = ""
	}
	pw := a.hash != nil || a.password != ""
	switch {
	case o.ProxySecret != "" && pw:
		a.mode = modeProxyPassword
	case o.ProxySecret != "":
		a.mode = modeProxy
	case pw:
		a.mode = modePassword
	case o.Off:
		a.mode = modeNone
	default:
		return nil, errors.New("API открыт: задайте MCPRIG_PASSWORD или MCPRIG_AUTH=off")
	}
	s, err := newSessionStore()
	if err != nil {
		return nil, err
	}
	a.sessions = s
	a.loginFails = map[string]*failWindow{}
	a.loginSem = make(chan struct{}, 1)
	return a, nil
}

// loginEnabled — есть ли страница входа (иначе GET /login → 404).
func (a *auth) loginEnabled() bool {
	return a.mode == modePassword || a.mode == modeProxyPassword
}

// hostOK — Host обязан быть в списке. Пустой список значит loopback, как в
// internal/httplisten: «не настроено» не должно означать «любой хост».
func (a *auth) hostOK(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	allowed := a.allowedHosts
	if len(allowed) == 0 {
		allowed = []string{"127.0.0.1", "localhost", "::1"}
	}
	for _, h := range allowed {
		if h == "*" || strings.ToLower(h) == host {
			return true
		}
	}
	return false
}

func (a *auth) proxyOK(r *http.Request) bool {
	user := r.Header.Get(a.userHeader)
	if user == "" || a.proxySecret == "" {
		return false
	}
	secret := r.Header.Get("x-mcp-proxy-secret")
	return subtle.ConstantTimeCompare([]byte(secret), []byte(a.proxySecret)) == 1
}

func (a *auth) sessionOK(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return session{}, false
	}
	return a.sessions.get(c.Value)
}

// check решает, пускать ли запрос. hostOK проверяется вызывающим (нужен 403, а
// не 401). user пуст для режима none.
func (a *auth) check(r *http.Request) (user string, ok bool) {
	switch a.mode {
	case modeNone:
		return "", true
	case modeProxy:
		if a.proxyOK(r) {
			return r.Header.Get(a.userHeader), true
		}
	case modePassword:
		if sess, found := a.sessionOK(r); found {
			return sess.user, true
		}
	case modeProxyPassword:
		if a.proxyOK(r) {
			return r.Header.Get(a.userHeader), true
		}
		if sess, found := a.sessionOK(r); found {
			return sess.user, true
		}
	}
	return "", false
}

func (a *auth) login(user, password string) bool {
	if subtle.ConstantTimeCompare([]byte(user), []byte(a.user)) != 1 {
		return false
	}
	if a.hash != nil {
		return a.hash.verify(password)
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1
}

// clientIP — адрес клиента для счётчика неудачных входов.
// clientIP — адрес для лимита входа. За обратным прокси RemoteAddr один на
// всех, поэтому X-Forwarded-For читаем только когда прокси настроен явно:
// иначе заголовок подделывается и лимит обходится.
func (a *auth) clientIP(r *http.Request) string {
	if a.proxySecret != "" {
		// Берём последний адрес: его дописал ближайший прокси. Первый элемент
		// приходит от клиента и подделывается, обходя лимит входа.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// secureRequest — панель обычно стоит за TLS-прокси, где до Go доходит http.
func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// loginBlocked — попытки с этого IP исчерпаны; возвращает остаток окна.
func (a *auth) loginBlocked(ip string) (time.Duration, bool) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	w := a.loginFails[ip]
	if w == nil {
		return 0, false
	}
	now := time.Now()
	if now.After(w.reset) {
		delete(a.loginFails, ip)
		return 0, false
	}
	if w.count >= loginMaxFails {
		return w.reset.Sub(now), true
	}
	return 0, false
}

func (a *auth) loginFailed(ip string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	now := time.Now()
	w := a.loginFails[ip]
	if w == nil || now.After(w.reset) {
		w = &failWindow{reset: now.Add(loginFailWindow)}
		a.loginFails[ip] = w
	}
	w.count++
	if len(a.loginFails) > 1024 {
		for k, v := range a.loginFails {
			if now.After(v.reset) {
				delete(a.loginFails, k)
			}
		}
	}
}

func (a *auth) loginOK(ip string) {
	a.loginMu.Lock()
	delete(a.loginFails, ip)
	a.loginMu.Unlock()
}

// loginCheck — scrypt под семафором: одновременных проверок не больше одной.
func (a *auth) loginCheck(user, password string) bool {
	a.loginSem <- struct{}{}
	defer func() { <-a.loginSem }()
	return a.login(user, password)
}

func (a *auth) startSession(w http.ResponseWriter, r *http.Request) (string, error) {
	value := a.sessions.create(a.user)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(sessionTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
	return a.user, nil
}

func (a *auth) clearSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.sessions.destroy(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureRequest(r),
	})
}
