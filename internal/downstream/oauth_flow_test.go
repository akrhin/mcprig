package downstream

// Ворота этапа 2: вся цепочка OAuth против фальшивого authorization server —
// 401 → protected-resource metadata → AS metadata → DCR → браузер → loopback →
// обмен кода → сессия; затем переиспользование токена и его обновление по
// refresh_token. Сеть только loopback.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/sintez/mcp-manager/internal/config"
	"github.com/sintez/mcp-manager/internal/oauth"
)

// fakeAS — фальшивый authorization server и защищённый MCP-эндпоинт на одном
// httptest-сервере: issuer совпадает с базовым URL, DCR настоящий.
type fakeAS struct {
	t          *testing.T
	srv        *httptest.Server
	base       string
	mcpURL     string
	mcpHandler http.Handler

	mu            sync.Mutex
	authorizeHits int
	registerHits  int
	refreshGrants int
	code          string
	challenge     string
	redirectURI   string
	access        []string
	refresh       map[string]bool
	n             int
	problems      []string
}

func newFakeAS(t *testing.T) *fakeAS {
	t.Helper()
	f := &fakeAS{t: t, refresh: map[string]bool{}}
	f.mcpHandler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newEchoServer()
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", f.handlePRM)
	mux.HandleFunc("/.well-known/oauth-authorization-server", f.handleASMeta)
	mux.HandleFunc("/register", f.handleRegister)
	mux.HandleFunc("/authorize", f.handleAuthorize)
	mux.HandleFunc("/token", f.handleToken)
	mux.HandleFunc("/mcp", f.handleMCP)

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.base = f.srv.URL
	f.mcpURL = f.srv.URL + "/mcp"
	return f
}

func newEchoServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0.1"}, nil)
	s.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "echo text",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	return s
}

func (f *fakeAS) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// problem запоминает причину отказа, чтобы тест напечатал её при падении.
func (f *fakeAS) problem(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	f.mu.Lock()
	f.problems = append(f.problems, msg)
	f.mu.Unlock()
	f.t.Logf("fake AS: %s", msg)
}

func (f *fakeAS) handlePRM(w http.ResponseWriter, _ *http.Request) {
	f.writeJSON(w, http.StatusOK, map[string]any{
		"resource":              f.mcpURL,
		"authorization_servers": []string{f.base},
		"scopes_supported":      []string{"mcp"},
	})
}

func (f *fakeAS) handleASMeta(w http.ResponseWriter, _ *http.Request) {
	f.writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                           f.base,
		"authorization_endpoint":           f.base + "/authorize",
		"token_endpoint":                   f.base + "/token",
		"registration_endpoint":            f.base + "/register",
		"scopes_supported":                 []string{"mcp"},
		"code_challenge_methods_supported": []string{"S256"},
		"authorization_response_iss_parameter_supported": true,
		"token_endpoint_auth_methods_supported":          []string{"none"},
	})
}

func (f *fakeAS) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		f.problem("register: метод %s", r.Method)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var meta struct {
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		f.problem("register: тело %v", err)
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.registerHits++
	if len(meta.RedirectURIs) > 0 {
		f.redirectURI = meta.RedirectURIs[0]
	}
	redirects := append([]string(nil), meta.RedirectURIs...)
	f.mu.Unlock()

	f.writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  "test-client",
		"redirect_uris":              redirects,
		"token_endpoint_auth_method": "none",
	})
}

func (f *fakeAS) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	f.authorizeHits++
	registered := f.redirectURI
	f.mu.Unlock()

	redirect := q.Get("redirect_uri")
	switch {
	case q.Get("code_challenge") == "":
		f.problem("authorize: нет code_challenge")
		http.Error(w, "missing code_challenge", http.StatusBadRequest)
		return
	case q.Get("code_challenge_method") != "S256":
		f.problem("authorize: code_challenge_method=%q", q.Get("code_challenge_method"))
		http.Error(w, "bad code_challenge_method", http.StatusBadRequest)
		return
	case q.Get("state") == "":
		f.problem("authorize: нет state")
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	case redirect == "" || (registered != "" && redirect != registered):
		f.problem("authorize: redirect_uri %q, зарегистрирован %q", redirect, registered)
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.n++
	f.code = fmt.Sprintf("code-%d", f.n)
	f.challenge = q.Get("code_challenge")
	code := f.code
	f.mu.Unlock()

	target, err := url.Parse(redirect)
	if err != nil {
		f.problem("authorize: redirect_uri %q: %v", redirect, err)
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	vals := target.Query()
	vals.Set("code", code)
	vals.Set("state", q.Get("state"))
	vals.Set("iss", f.base)
	target.RawQuery = vals.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (f *fakeAS) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.problem("token: форма %v", err)
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if got := r.PostForm.Get("resource"); got != f.mcpURL {
		// RFC 8707: SDK обязан прислать resource — фиксируем факт, но не отбиваем.
		f.t.Logf("token: resource=%q (ожидался %q)", got, f.mcpURL)
	}

	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if !f.checkCode(r.PostForm.Get("code"), r.PostForm.Get("code_verifier")) {
			f.tokenError(w, "invalid_grant", "code или code_verifier не совпали")
			return
		}
		f.issue(w, "authorization_code")
	case "refresh_token":
		rt := r.PostForm.Get("refresh_token")
		f.mu.Lock()
		ok := f.refresh[rt]
		f.mu.Unlock()
		if !ok {
			f.tokenError(w, "invalid_grant", "refresh_token неизвестен")
			return
		}
		f.mu.Lock()
		f.refreshGrants++
		f.mu.Unlock()
		f.issue(w, "refresh_token")
	default:
		f.tokenError(w, "unsupported_grant_type", r.PostForm.Get("grant_type"))
	}
}

func (f *fakeAS) checkCode(code, verifier string) bool {
	f.mu.Lock()
	wantChallenge, wantCode := f.challenge, f.code
	f.code, f.challenge = "", ""
	f.mu.Unlock()
	if code == "" || code != wantCode || verifier == "" || wantChallenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == wantChallenge
}

func (f *fakeAS) issue(w http.ResponseWriter, grant string) {
	f.mu.Lock()
	f.n++
	access := fmt.Sprintf("access-%s-%d", grant, f.n)
	refresh := fmt.Sprintf("refresh-%d", f.n)
	f.access = []string{access} // прежние access-токены сразу недействительны
	f.refresh = map[string]bool{refresh: true}
	f.mu.Unlock()

	f.writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func (f *fakeAS) tokenError(w http.ResponseWriter, code, desc string) {
	f.problem("token: %s: %s", code, desc)
	f.writeJSON(w, http.StatusBadRequest, map[string]any{
		"error":             code,
		"error_description": desc,
	})
}

func (f *fakeAS) handleMCP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	valid := slices.Contains(f.access, token)
	f.mu.Unlock()
	if !valid {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata=%q`, f.base+"/.well-known/oauth-protected-resource"))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	f.mcpHandler.ServeHTTP(w, r)
}

// revokeAccess делает текущий access-токен недействительным: сервер начнёт
// отвечать на него 401, как после отзыва.
func (f *fakeAS) revokeAccess() {
	f.mu.Lock()
	f.access = nil
	f.mu.Unlock()
}

func (f *fakeAS) counters() (authorize, register, refresh int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authorizeHits, f.registerHits, f.refreshGrants
}

func (f *fakeAS) dump() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprintf("authorize=%d register=%d refresh=%d problems=%v",
		f.authorizeHits, f.registerHits, f.refreshGrants, f.problems)
}

func TestOAuthFlowAgainstFakeAS(t *testing.T) {
	dir := t.TempDir()
	store, err := oauth.OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	f := newFakeAS(t)

	// Порт loopback выбираем свободный: config.OAuth.CallbackPort подменяет 8947.
	port := freePort(t)

	var browserMu sync.Mutex
	var browsed []string
	oldBrowser := oauth.BrowserOpen
	oauth.BrowserOpen = func(raw string) error {
		browserMu.Lock()
		browsed = append(browsed, raw)
		browserMu.Unlock()
		// Браузер: GET /authorize без ручного разбора — клиент сам идёт по 302
		// на loopback-приёмник.
		resp, err := http.Get(raw)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("страница приёмника: %s", resp.Status)
		}
		return nil
	}
	t.Cleanup(func() { oauth.BrowserOpen = oldBrowser })

	browserCount := func() int {
		browserMu.Lock()
		defer browserMu.Unlock()
		return len(browsed)
	}

	srv := &config.Server{
		Name:      "fake",
		Transport: config.TransportHTTP,
		URL:       f.mcpURL,
		OAuth:     &config.OAuth{CallbackPort: port},
	}
	conn := &Connector{Store: store, Version: "test"}

	// 1. Первый коннект: полный поток с DCR, браузером и обменом кода.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel1()
	sess1, err := conn.Connect(ctx1, srv)
	if err != nil {
		t.Fatalf("первый Connect: %v\n%s", err, f.dump())
	}
	if got := toolNames(t, sess1); !slices.Contains(got, "echo") {
		t.Fatalf("tools = %v, want echo\n%s", got, f.dump())
	}
	sess1.Close()
	if n := browserCount(); n != 1 {
		t.Fatalf("браузер открыт %d раз, want 1\n%s", n, f.dump())
	}
	az, reg, _ := f.counters()
	if az != 1 || reg != 1 {
		t.Fatalf("authorize=%d register=%d, want 1/1\n%s", az, reg, f.dump())
	}

	// Файл токенов: 0600 и refresh-токен на месте.
	tokenPath := filepath.Join(dir, "oauth.json")
	for _, p := range []string{tokenPath, filepath.Join(dir, "oauth-clients.json")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("файл %s: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("права %s = %o, want 600", p, perm)
		}
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]oauth2.Token
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("файл токенов: %v", err)
	}
	key := oauth.Key("fake", f.mcpURL)
	first := saved[key]
	if first.AccessToken == "" || first.RefreshToken == "" || first.TokenType != "Bearer" {
		t.Fatalf("токен в файле неполный: %+v", first)
	}

	// 2. Второй коннект с тем же Store: браузер не открывается.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	sess2, err := conn.Connect(ctx2, srv)
	if err != nil {
		t.Fatalf("второй Connect: %v\n%s", err, f.dump())
	}
	if got := toolNames(t, sess2); !slices.Contains(got, "echo") {
		t.Fatalf("второй tools = %v\n%s", got, f.dump())
	}
	sess2.Close()
	if n := browserCount(); n != 1 {
		t.Fatalf("второй коннект открыл браузер (%d) — токен не переиспользован\n%s", n, f.dump())
	}

	// 3. AS больше не принимает прежний access-токен, сохранённый токен
	// просрочен: клиент обязан обновить его по refresh_token, без браузера.
	f.revokeAccess()
	expired, ok := store.Load(key)
	if !ok {
		t.Fatalf("нет сохранённого токена\n%s", f.dump())
	}
	expired.Expiry = time.Now().Add(-time.Minute)
	if err := store.Save(key, expired); err != nil {
		t.Fatalf("испортить expiry: %v", err)
	}

	ctx3, cancel3 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel3()
	sess3, err := conn.Connect(ctx3, srv)
	if err != nil {
		t.Fatalf("третий Connect (refresh): %v\n%s", err, f.dump())
	}
	if got := toolNames(t, sess3); !slices.Contains(got, "echo") {
		t.Fatalf("третий tools = %v\n%s", got, f.dump())
	}
	sess3.Close()

	az, _, refresh := f.counters()
	if refresh != 1 {
		t.Fatalf("refresh grant = %d, want 1\n%s", refresh, f.dump())
	}
	if az != 1 {
		t.Fatalf("authorize=%d — третий коннект ушёл в браузер вместо refresh\n%s", az, f.dump())
	}
	if n := browserCount(); n != 1 {
		t.Fatalf("третий коннект открыл браузер %d раз\n%s", n, f.dump())
	}

	renewed, ok := store.Load(key)
	if !ok || renewed.AccessToken == first.AccessToken || renewed.RefreshToken == first.RefreshToken {
		t.Fatalf("токен не обновлён в хранилище: %+v\n%s", renewed, f.dump())
	}
}

func toolNames(t *testing.T, sess *mcp.ClientSession) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var names []string
	for tool, err := range sess.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("tools/list: %v", err)
		}
		names = append(names, tool.Name)
	}
	return names
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("свободный порт: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
