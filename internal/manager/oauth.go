package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/downstream"
	"github.com/akrhin/mcprig/internal/oauth"
)

// authFlowTimeout — сколько панель ждёт, пока человек пройдёт вход по ссылке.
// Пятнадцать секунд health-пробы тут не годятся: браузер у него, а не у нас.
const authFlowTimeout = 10 * time.Minute

// authFlow — состояние входа одного сервера одного набора.
type authFlow struct {
	State   string `json:"state"` // idle | pending | ok | error
	URL     string `json:"url,omitempty"`
	Message string `json:"message,omitempty"`
}

type authOutcome struct {
	res *oauth.Result
	err error
}

func authKey(setID, server string) string { return setID + "\x00" + server }

// apiServerAuthorize заводит вход по OAuth для сервера набора. POST начинает,
// GET отдаёт состояние — так панель не держит долгий запрос открытым.
func (m *Manager) apiServerAuthorize(req *request) error {
	setID, name := req.args[0], req.args[1]
	set := m.store.Set(setID)
	if set == nil {
		return notFound("набор " + setID + " не найден")
	}
	srv := m.store.Server(name)
	if srv == nil {
		return notFound("сервер " + name + " не найден")
	}
	if req.r.Method == http.MethodGet {
		writeJSON(req.w, http.StatusOK, m.flowView(authKey(setID, name)))
		return nil
	}
	inSet := false
	for _, n := range set.Servers {
		if n == name {
			inSet = true
		}
	}
	if !inSet {
		return badRequest("сервер " + name + " не входит в набор " + setID)
	}
	if srv.URL == "" {
		return badRequest("OAuth поддерживается только у удалённого сервера (transport: http)")
	}
	if cur := m.flowView(authKey(setID, name)); cur.State == "pending" {
		writeJSON(req.w, http.StatusAccepted, cur)
		return nil
	}
	redirect, err := m.callbackURL()
	if err != nil {
		return err
	}
	key := authKey(setID, name)
	m.setFlow(key, authFlow{State: "pending"})
	go m.authorize(setID, srv, redirect)
	writeJSON(req.w, http.StatusAccepted, m.flowView(key))
	return nil
}

// authorize повторяет подключение сервера тем же соединителем, что и набор:
// без токена он упрётся в 401 и попросит ссылку, ссылку получит панель.
func (m *Manager) authorize(setID string, srv *config.Server, redirect string) {
	key := authKey(setID, srv.Name)
	ctx, cancel := context.WithTimeout(m.ctx, authFlowTimeout)
	defer cancel()

	store, err := oauth.OpenStore(filepath.Join(m.opts.DataDir, "sets", setID))
	if err != nil {
		m.finishFlow(key, err)
		return
	}
	own := config.OAuth{}
	if srv.OAuth != nil {
		own = *srv.OAuth
	}
	// Адрес приёма кода задаёт панель: её сервер уже слушает тот адрес, по
	// которому человек открыл панель в браузере.
	own.RedirectURL = redirect
	own.CallbackPort = 0
	target := *srv
	target.OAuth = &own

	conn := &downstream.Connector{
		Version: m.version,
		Store:   store,
		Log:     m.log,
		Browser: func(string) error { return nil },
		Authorize: func(ctx context.Context, authURL string) (*oauth.Result, error) {
			return m.waitAuthorization(ctx, key, authURL)
		},
	}
	session, err := conn.Connect(ctx, &target)
	if err == nil {
		session.Close()
	}
	if err != nil {
		m.finishFlow(key, err)
		return
	}

	msg := "сервер подключился без OAuth — токен не понадобился"
	if _, ok := store.Load(oauth.Key(srv.Name, srv.URL)); ok {
		msg = "токен сохранён"
	}
	// Набор читает токен при старте: без перезапуска сервер останется 401.
	if set := m.store.Set(setID); set != nil {
		if _, rerr := m.RestartSet(context.Background(), set); rerr != nil {
			msg += "; перезапуск набора не удался: " + rerr.Error()
		}
	}
	m.finishFlowMessage(key, msg)
}

// waitAuthorization отдаёт URL панели и ждёт код из её HTTP-приёмника.
func (m *Manager) waitAuthorization(ctx context.Context, key, authURL string) (*oauth.Result, error) {
	state := authURLState(authURL)
	if state == "" {
		return nil, errors.New("в URL авторизации нет параметра state")
	}
	ch := make(chan authOutcome, 1)
	m.authMu.Lock()
	if m.authWait == nil {
		m.authWait = map[string]chan authOutcome{}
	}
	if _, busy := m.authWait[state]; busy {
		m.authMu.Unlock()
		return nil, errors.New("этот вход уже выполняется — дождитесь ответа")
	}
	m.authWait[state] = ch
	if m.authFlows == nil {
		m.authFlows = map[string]*authFlow{}
	}
	if f, ok := m.authFlows[key]; ok {
		f.URL = authURL
	} else {
		m.authFlows[key] = &authFlow{State: "pending", URL: authURL}
	}
	m.authMu.Unlock()
	defer func() {
		m.authMu.Lock()
		delete(m.authWait, state)
		m.authMu.Unlock()
	}()

	select {
	case o := <-ch:
		return o.res, o.err
	case <-ctx.Done():
		return nil, fmt.Errorf("ссылка не использована за %s — код не получен; начните вход заново", humanDuration(authFlowTimeout))
	}
}

// handleAuthCallback принимает редирект сервера авторизации. Публичный маршрут:
// у браузера нет ни сессии панели, ни её куки, а state уже привязан к входу.
func (m *Manager) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		m.methodNotAllowed(w, []string{http.MethodGet})
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	m.authMu.Lock()
	ch := m.authWait[state]
	m.authMu.Unlock()
	if ch == nil {
		http.Error(w, "ссылка устарела или вход уже завершён — начните вход заново", http.StatusBadRequest)
		return
	}
	if e := q.Get("error"); e != "" {
		msg := e
		if d := q.Get("error_description"); d != "" {
			msg += ": " + d
		}
		deliverAuth(ch, authOutcome{err: fmt.Errorf("сервер авторизации вернул ошибку: %s", msg)})
		http.Error(w, "авторизация отклонена: "+msg, http.StatusBadRequest)
		return
	}
	code := q.Get("code")
	if code == "" {
		deliverAuth(ch, authOutcome{err: errors.New("в ответе сервера нет параметра code")})
		http.Error(w, "в ответе сервера нет параметра code", http.StatusBadRequest)
		return
	}
	deliverAuth(ch, authOutcome{res: &oauth.Result{Code: code, State: state, Iss: q.Get("iss")}})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, authDonePage)
}

func deliverAuth(ch chan authOutcome, o authOutcome) {
	select {
	case ch <- o:
	default:
	}
}

// CallbackURL — адрес приёма кода. Заданный publicUrl — единственный источник
// (префикс пути сохраняется: https://host/prefix → https://host/prefix/oauth/
// callback). Без него берётся хост и порт панели, по которым её и открыли,
// чтобы человеку не пробрасывать порты.
func CallbackURL(publicURL, host string, port int) (string, error) {
	if publicURL != "" {
		return publicURL + "/oauth/callback", nil
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = lanIPv4()
	}
	if host == "" {
		return "", errors.New("не удалось определить адрес панели для приёма кода: задайте host в настройках")
	}
	return fmt.Sprintf("http://%s:%d/oauth/callback", host, port), nil
}

func (m *Manager) callbackURL() (string, error) {
	return CallbackURL(m.opts.PublicURL, m.opts.Host, m.opts.Port)
}

func lanIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

func authURLState(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get("state")
}

func (m *Manager) flowView(key string) authFlow {
	m.authMu.Lock()
	defer m.authMu.Unlock()
	if f, ok := m.authFlows[key]; ok {
		return *f
	}
	return authFlow{State: "idle"}
}

func (m *Manager) setFlow(key string, f authFlow) {
	m.authMu.Lock()
	defer m.authMu.Unlock()
	if m.authFlows == nil {
		m.authFlows = map[string]*authFlow{}
	}
	m.authFlows[key] = &f
}

func (m *Manager) finishFlow(key string, err error) {
	m.authMu.Lock()
	defer m.authMu.Unlock()
	if f, ok := m.authFlows[key]; ok {
		f.State = "error"
		f.URL = ""
		f.Message = err.Error()
	}
}

func (m *Manager) finishFlowMessage(key, msg string) {
	m.authMu.Lock()
	defer m.authMu.Unlock()
	if f, ok := m.authFlows[key]; ok {
		f.State = "ok"
		f.URL = ""
		f.Message = msg
	}
}

const authDonePage = `<!doctype html>
<html lang="ru">
<head><meta charset="utf-8"><title>Авторизация завершена</title></head>
<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#06171C;color:#E6EFF1;font:16px/1.5 system-ui,sans-serif">
<main style="text-align:center;padding:24px">
<h1 style="font-size:20px;margin:0 0 8px">Авторизация завершена</h1>
<p style="margin:0">Вернитесь в панель — вкладку можно закрыть.</p>
</main>
</body>
</html>
`
