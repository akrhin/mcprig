package oauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os/exec"
	"runtime"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// Handler — OAuth-обработчик MCP-транспорта: SDK-обработчик (методы продвинуты
// встраиванием) плюс адрес приёмника, выведенный из redirect_uri.
type Handler struct {
	*auth.AuthorizationCodeHandler
	addr  string
	fetch func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error)
}

// Addr — loopback-адрес приёмника кода из redirect_uri (для логов и проверок).
func (h *Handler) Addr() string { return h.addr }

// Options — параметры фабрики обработчика.
type Options struct {
	ServerName   string
	ServerURL    string
	RedirectURL  string // обязателен, http://127.0.0.1:<port>/callback — порт задаёт приёмник
	ClientID     string // если пуст — динамическая регистрация клиента
	ClientSecret string
	Scopes       []string
	Browser      func(url string) error // nil — openBrowser
	Store        *Store                 // nil — токены не сохраняются
	Log          *slog.Logger
}

// callbackAddr — адрес приёмника из redirect_uri: браузер вернётся ровно на
// указанный там порт, поэтому слушать другой — гарантированный пятиминутный
// таймаут. Без порта (или с ":0") возвращаться некуда — это ошибка конфигурации,
// а не повод слушать эфемерный порт.
func callbackAddr(redirectURL string) (string, error) {
	u, err := url.Parse(redirectURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("oauth: неверный RedirectURL %q", redirectURL)
	}
	host, port := u.Hostname(), u.Port()
	if port == "" || port == "0" {
		return "", errors.New("oauth: redirectUrl без порта — браузеру некуда вернуться, укажите http://127.0.0.1:<port>/callback")
	}
	return net.JoinHostPort(host, port), nil
}

// NewHandler собирает auth.AuthorizationCodeHandler: обнаружение метаданных,
// DCR, PKCE, обмен кода и refresh делаются go-sdk, здесь — только регистрация
// клиента, хранение токенов и приём редиректа.
func NewHandler(_ context.Context, o Options) (*Handler, error) {
	if o.RedirectURL == "" {
		return nil, errors.New("oauth: RedirectURL обязателен")
	}
	if o.ServerName == "" {
		return nil, errors.New("oauth: не указано имя сервера")
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	browser := o.Browser
	if browser == nil {
		browser = BrowserOpen
	}
	addr, err := callbackAddr(o.RedirectURL)
	if err != nil {
		return nil, err
	}
	key := Key(o.ServerName, o.ServerURL)

	fetch := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		log.Info("требуется авторизация MCP-сервера: откройте URL в браузере",
			"server", o.ServerName, "url", args.URL)
		cb, err := NewCallback(addr, stateFromURL(args.URL))
		if err != nil {
			return nil, fmt.Errorf("oauth %s: %w", o.ServerName, err)
		}
		defer cb.Close()
		if err := browser(args.URL); err != nil {
			// URL остаётся в логе — его можно открыть вручную.
			log.Warn("не удалось открыть браузер, откройте URL из лога вручную",
				"server", o.ServerName, "err", err)
		}
		res, err := cb.Wait(ctx)
		if err != nil {
			return nil, fmt.Errorf("oauth %s: %w", o.ServerName, err)
		}
		return &auth.AuthorizationResult{Code: res.Code, State: res.State, Iss: res.Iss}, nil
	}

	cfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              o.RedirectURL,
		RequestRefreshToken:      true,
		AuthorizationCodeFetcher: fetch,
	}

	if o.ClientID != "" {
		creds := &oauthex.ClientCredentials{ClientID: o.ClientID}
		if o.ClientSecret != "" {
			creds.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: o.ClientSecret}
		}
		cfg.PreregisteredClient = creds
	} else {
		cfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:              "mcprig",
				RedirectURIs:            []string{o.RedirectURL},
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				TokenEndpointAuthMethod: "none",
			},
		}
	}

	if len(o.Scopes) > 0 {
		cfg.ScopeFilter = func([]string) []string { return o.Scopes }
	}

	if o.Store != nil {
		if tok, ok := o.Store.Load(key); ok {
			log.Info("используется сохранённый OAuth-токен",
				"server", o.ServerName, "expiry", tok.Expiry)
			cfg.InitialTokenSource = savedTokenSource(o.Store, key, tok, log, o.ServerName)
		}
	}

	cfg.NewTokenSource = func(ctx context.Context, oc *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
		src := oc.TokenSource(ctx, tok)
		if o.Store == nil {
			return src, nil
		}
		if err := o.Store.Save(key, tok); err != nil {
			log.Warn("не удалось сохранить OAuth-токен", "server", o.ServerName, "err", err)
		}
		err := o.Store.SaveClient(key, ClientInfo{
			ClientID:     oc.ClientID,
			ClientSecret: oc.ClientSecret,
			TokenURL:     oc.Endpoint.TokenURL,
			AuthStyle:    oc.Endpoint.AuthStyle,
		})
		if err != nil {
			log.Warn("не удалось сохранить регистрацию OAuth-клиента", "server", o.ServerName, "err", err)
		}
		return &persistingSource{base: src, store: o.Store, key: key, last: tok.AccessToken, log: log, server: o.ServerName}, nil
	}

	h, err := auth.NewAuthorizationCodeHandler(cfg)
	if err != nil {
		return nil, fmt.Errorf("oauth %s: %w", o.ServerName, err)
	}
	return &Handler{AuthorizationCodeHandler: h, addr: addr, fetch: fetch}, nil
}

// savedTokenSource отдаёт сохранённый токен и, если для него известны
// регистрация клиента и token endpoint, обновляет его по refresh_token без
// браузера. Без этих данных token endpoint неизвестен до Authorize, поэтому
// просроченный токен даст 401/403 и обычный поток авторизации.
func savedTokenSource(store *Store, key string, tok *oauth2.Token, log *slog.Logger, server string) oauth2.TokenSource {
	info, ok := store.LoadClient(key)
	if !ok || info.TokenURL == "" || info.ClientID == "" {
		return oauth2.StaticTokenSource(tok)
	}
	cfg := &oauth2.Config{
		ClientID:     info.ClientID,
		ClientSecret: info.ClientSecret,
		Endpoint:     oauth2.Endpoint{TokenURL: info.TokenURL, AuthStyle: info.AuthStyle},
	}
	// Фоновый контекст: источник живёт дольше запроса, который его создал.
	return &persistingSource{
		base:   cfg.TokenSource(context.Background(), tok),
		store:  store,
		key:    key,
		last:   tok.AccessToken,
		log:    log,
		server: server,
	}
}

// persistingSource пишет в хранилище обновлённый токен, но только когда
// сменился access_token.
type persistingSource struct {
	mu     sync.Mutex
	base   oauth2.TokenSource
	store  *Store
	key    string
	last   string
	log    *slog.Logger
	server string
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.base.Token()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	changed := tok.AccessToken != p.last
	p.last = tok.AccessToken
	p.mu.Unlock()
	if !changed {
		return tok, nil
	}
	if err := p.store.Save(p.key, tok); err != nil {
		p.log.Warn("не удалось сохранить обновлённый OAuth-токен", "server", p.server, "err", err)
	}
	return tok, nil
}

// stateFromURL достаёт state из URL авторизации, чтобы приёмник кода мог
// отклонить чужой редирект.
func stateFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get("state")
}

// BrowserOpen открывает URL авторизации. Переменная, а не параметр, потому что
// Connector в internal/downstream не имеет поля для браузера: тесты подменяют
// её, чтобы не запускать настоящий.
var BrowserOpen = openBrowser

// openBrowser запускает URL в браузере по умолчанию, не дожидаясь его закрытия.
func openBrowser(raw string) error {
	var name string
	switch runtime.GOOS {
	case "linux":
		name = "xdg-open"
	case "darwin":
		name = "open"
	default:
		return fmt.Errorf("открытие браузера не поддерживается на %s", runtime.GOOS)
	}
	return exec.Command(name, raw).Start()
}
