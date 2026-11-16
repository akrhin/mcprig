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

	"github.com/akrhin/mcprig/internal/httplisten"
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
	// TrustedProxies — пиры, чьим X-Forwarded-* верим: IP или CIDR через
	// запятую. Пусто — только loopback: прокси обычно на этой же машине.
	TrustedProxies string
	User           string
	UserHeader     string
	AllowedHosts   []string
	Off            bool
	// Secure ставит cookie Secure независимо от r.TLS: панель за TLS-прокси
	// видит http, а публичный адрес (или собственный TLS) говорит https.
	Secure bool
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
	trustedNets  []*net.IPNet
	secure       bool
	user         string
	userHeader   string
	allowedHosts []string
	sessions     *sessionStore

	// Защита /api/login: счётчик неудач по IP и ограниченный параллелизм scrypt.
	loginMu      sync.Mutex
	loginFails   map[string]*failWindow
	loginSem     chan struct{}
	loginPending int
}

type failWindow struct {
	count int
	reset time.Time
}

const (
	loginMaxFails   = 5
	loginFailWindow = time.Minute

	// loginMaxConcurrent — сколько scrypt считается одновременно; остальные ждут.
	loginMaxConcurrent = 2
	// loginMaxPending — предел считающих и ждущих вместе; сверх него сразу 429.
	loginMaxPending = 8
)

// parseTrustedProxies читает MCPRIG_TRUSTED_PROXIES: адреса пиров, чьим
// X-Forwarded-* можно верить. Ошибка в записи валит старт: молча сузить
// доверие до loopback значило бы уронить вход за прокси уже в бою.
func parseTrustedProxies(list string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("менеджер: MCPRIG_TRUSTED_PROXIES: не IP и не CIDR: %q", item)
			}
			bits := 8 * net.IPv6len
			if v4 := ip.To4(); v4 != nil {
				ip, bits = v4, 8*net.IPv4len
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("менеджер: MCPRIG_TRUSTED_PROXIES: %q: %w", item, err)
		}
		out = append(out, n)
	}
	return out, nil
}

func newAuth(o AuthOptions) (*auth, error) {
	trusted, err := parseTrustedProxies(o.TrustedProxies)
	if err != nil {
		return nil, err
	}
	a := &auth{
		password:     o.Password,
		proxySecret:  o.ProxySecret,
		trustedNets:  trusted,
		secure:       o.Secure,
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
	a.loginSem = make(chan struct{}, loginMaxConcurrent)
	return a, nil
}

// loginEnabled — есть ли страница входа (иначе GET /login → 404).
func (a *auth) loginEnabled() bool {
	return a.mode == modePassword || a.mode == modeProxyPassword
}

// hostOK — Host обязан быть в списке. Политика одна на панель и на набор:
// httplisten.HostAllowed, где пустой список значит loopback, а не «любой хост».
func (a *auth) hostOK(host string) bool {
	return httplisten.HostAllowed(host, a.allowedHosts)
}

func (a *auth) proxyOK(r *http.Request) bool {
	user := r.Header.Get(a.userHeader)
	if user == "" || a.proxySecret == "" {
		return false
	}
	secret := r.Header.Get("x-mcp-proxy-secret")
	return subtle.ConstantTimeCompare([]byte(secret), []byte(a.proxySecret)) == 1
}

// trustsProxy — верить ли X-Forwarded-* этого запроса. Секрет говорит «прокси
// настроен», но не «запрос пришёл от него»: заголовки от чужого пира подделали
// бы публичное имя, схему и адрес клиента.
func (a *auth) trustsProxy(r *http.Request) bool {
	if a.proxySecret == "" {
		return false
	}
	ip := net.ParseIP(httplisten.HostOnly(r.RemoteAddr))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, n := range a.trustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *auth) sessionOK(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	_, found := a.sessions.get(c.Value)
	return found
}

// check решает, пускать ли запрос. hostOK проверяется вызывающим (нужен 403, а
// не 401).
func (a *auth) check(r *http.Request) bool {
	switch a.mode {
	case modeNone:
		return true
	case modeProxy:
		if a.proxyOK(r) {
			return true
		}
	case modePassword:
		if a.sessionOK(r) {
			return true
		}
	case modeProxyPassword:
		if a.proxyOK(r) {
			return true
		}
		if a.sessionOK(r) {
			return true
		}
	}
	return false
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

// clientIP — адрес клиента за прокси. X-Forwarded-For читаем только от
// доверенного пира: иначе заголовок подделывается и лимит входа обходится.
func (a *auth) clientIP(r *http.Request) string {
	if a.trustsProxy(r) {
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

// loginIPs — ключ лимита входа. За доверенным прокси адрес сокета один на
// всех клиентов, поэтому считаем по адресу из X-Forwarded-For (его дописал
// прокси); иначе — по сокету, иначе заголовок обходил бы лимит.
func (a *auth) loginIPs(r *http.Request) []string {
	socket := httplisten.HostOnly(r.RemoteAddr)
	if a.trustsProxy(r) {
		if ip := a.clientIP(r); ip != socket {
			return []string{ip}
		}
	}
	return []string{socket}
}

// secureRequest — панель обычно стоит за TLS-прокси, где до Go доходит http.
// X-Forwarded-Proto верим только доверенному пиру (trustsProxy): иначе любой
// клиент пометил бы свой http-запрос https.
func secureRequest(r *http.Request, trustProxy bool) bool {
	return r.TLS != nil || (trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

// loginBlocked — попытки с этих адресов исчерпаны; возвращает остаток самого
// длинного окна.
func (a *auth) loginBlocked(ips []string) (time.Duration, bool) {
	var wait time.Duration
	blocked := false
	for _, ip := range ips {
		if w, b := a.loginBlockedOne(ip); b && (!blocked || w > wait) {
			wait, blocked = w, true
		}
	}
	return wait, blocked
}

func (a *auth) loginBlockedOne(ip string) (time.Duration, bool) {
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

func (a *auth) loginFailed(ips []string) {
	for _, ip := range ips {
		a.loginFailedOne(ip)
	}
}

func (a *auth) loginFailedOne(ip string) {
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

func (a *auth) loginOK(ips []string) {
	for _, ip := range ips {
		a.loginOKOne(ip)
	}
}

func (a *auth) loginOKOne(ip string) {
	a.loginMu.Lock()
	delete(a.loginFails, ip)
	a.loginMu.Unlock()
}

// loginCheck — scrypt под семафором: одновременно не больше loginMaxConcurrent
// проверок, остальные ждут. Когда считающих и ждущих вместе уже loginMaxPending,
// отвечаем busy: очередь не растёт, CPU не выедается залпом первых попыток.
func (a *auth) loginCheck(user, password string) (ok, busy bool) {
	a.loginMu.Lock()
	if a.loginPending >= loginMaxPending {
		a.loginMu.Unlock()
		return false, true
	}
	a.loginPending++
	a.loginMu.Unlock()
	defer func() {
		a.loginMu.Lock()
		a.loginPending--
		a.loginMu.Unlock()
	}()

	a.loginSem <- struct{}{}
	defer func() { <-a.loginSem }()
	return a.login(user, password), false
}

func (a *auth) startSession(w http.ResponseWriter, r *http.Request) string {
	value := a.sessions.create(a.user)
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure по факту TLS — на http-панели литерал true убил бы вход
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(sessionTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secure || secureRequest(r, a.trustsProxy(r)),
	})
	return a.user
}

func (a *auth) clearSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.sessions.destroy(c.Value)
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure по факту TLS — на http-панели литерал true убил бы выход
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secure || secureRequest(r, a.trustsProxy(r)),
	})
}
