package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/downstream"
	"github.com/sintez/mcprig/internal/mcpserver"
	"github.com/sintez/mcprig/internal/oauth"
)

// errWarmProblems — строки уже напечатаны; main выходит с кодом 1 без лишней
// строки об ошибке, как у doctor.
var errWarmProblems = errors.New("warm: есть ошибки")

// warmConfig — всё, что нужно прогреву; в тестах подставляется целиком.
type warmConfig struct {
	URL       string
	Set       string
	All       bool
	Timeout   time.Duration
	Token     string
	TokenFile string
	Config    string
	JSON      bool
	Password  string
	Secret    string
	User      string

	// setOpen — набор открыт: отсутствующий токен не ошибка (только --all).
	setOpen bool
}

// warmResult — одна строка отчёта и один элемент -json.
type warmResult struct {
	Set   string  `json:"set"`
	Tools int     `json:"tools"`
	Ms    float64 `json:"ms"`
	Error string  `json:"error,omitempty"`
}

func newWarmFlags() (*flag.FlagSet, *warmConfig) {
	c := &warmConfig{}
	fs := flag.NewFlagSet("warm", flag.ContinueOnError)
	fs.StringVar(&c.URL, "url", "", "адрес набора, напр. http://127.0.0.1:9101/mcp; с --all — адрес панели")
	fs.StringVar(&c.Set, "set", "main", "имя набора: под ним берётся ~/.config/mcprig/<set>.token и печатается строка")
	fs.BoolVar(&c.All, "all", false, "все наборы из панели")
	fs.DurationVar(&c.Timeout, "timeout", 2*time.Minute, "бюджет одного набора")
	fs.StringVar(&c.Token, "token", "", "bearer-ключ набора (виден в ps; лучше --token-file)")
	fs.StringVar(&c.TokenFile, "token-file", "", "файл с bearer-ключом набора")
	fs.StringVar(&c.Config, "config", "", "локальный servers.json: прогрев без сети, через internal/mcpserver")
	fs.BoolVar(&c.JSON, "json", false, "машинный вывод")
	fs.StringVar(&c.Password, "password", "", "пароль панели (по умолчанию MCPRIG_PASSWORD)")
	fs.StringVar(&c.Secret, "secret", "", "секрет обратного прокси панели (по умолчанию MCPRIG_PROXY_SECRET)")
	fs.StringVar(&c.User, "user", "", "пользователь панели для входа (по умолчанию admin)")
	return fs, c
}

func runWarm(args []string) error {
	fs, o := newWarmFlags()
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	switch {
	case o.All && o.Config != "":
		return &usageError{"--all и --config несовместимы"}
	case o.All && o.Set != "main":
		return &usageError{"--all и --set несовместимы: прогреваются все наборы"}
	case o.Token != "" && o.TokenFile != "":
		return &usageError{"выберите что-то одно: --token или --token-file"}
	case o.Timeout <= 0:
		return &usageError{"--timeout должен быть положительным"}
	}

	var results []warmResult
	switch {
	case o.Config != "":
		results = append(results, warmLocal(o))
	case o.All:
		results = warmAll(o)
	default:
		results = append(results, warmOne(o, o.Set, o.URL))
	}

	var out []byte
	if o.JSON {
		body, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return err
		}
		out = append(body, '\n')
	} else {
		out = renderWarm(results)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		return err
	}
	for _, r := range results {
		if r.Error != "" {
			return errWarmProblems
		}
	}
	return nil
}

// warmOne греет один набор по HTTP. Пустой endpoint — резолв через панель по
// порту набора; --url экономит этот поход.
func warmOne(o *warmConfig, set, endpoint string) warmResult {
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()

	if endpoint == "" {
		var err error
		endpoint, err = endpointFor(ctx, o, set)
		if err != nil {
			return warmResult{Set: set, Error: err.Error()}
		}
	}
	return warmEndpoint(ctx, o, set, endpoint)
}

func warmEndpoint(ctx context.Context, o *warmConfig, set, endpoint string) warmResult {
	token, err := warmToken(o, set)
	if err != nil {
		if !o.setOpen {
			return warmResult{Set: set, Error: err.Error()}
		}
		token = ""
	}

	start := time.Now()
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: &bearerAuth{token: token}, Timeout: o.Timeout},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcprig-warm", Version: version}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return warmResult{Set: set, Error: err.Error(), Ms: elapsedMs(start)}
	}
	defer cs.Close()

	return finishWarm(ctx, set, start, cs)
}

// warmLocal греет набор без сети: тот же mcpserver, клиент — на in-memory
// транспорте. Токен не нужен, порт не нужен.
func warmLocal(o *warmConfig) warmResult {
	set := filepath.Base(filepath.Dir(o.Config))
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()

	cfg, err := config.Load(o.Config)
	if err != nil {
		return warmResult{Set: set, Error: err.Error()}
	}
	start := time.Now()
	store, err := oauth.OpenStore(stateDir())
	if err != nil {
		return warmResult{Set: set, Error: err.Error()}
	}
	conn := &downstream.Connector{Version: version, OAuth: cfg.OAuth, Store: store}
	mgr := mcpserver.New(cfg, version, conn)
	defer mgr.Close()

	st, ct := mcp.NewInMemoryTransports()
	if _, err := mgr.Server().Connect(ctx, st, nil); err != nil {
		return warmResult{Set: set, Error: err.Error(), Ms: elapsedMs(start)}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcprig-warm", Version: version}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return warmResult{Set: set, Error: err.Error(), Ms: elapsedMs(start)}
	}
	defer cs.Close()

	return finishWarm(ctx, set, start, cs)
}

func finishWarm(ctx context.Context, set string, start time.Time, cs *mcp.ClientSession) warmResult {
	tools, err := warmSession(ctx, cs)
	res := warmResult{Set: set, Tools: tools, Ms: elapsedMs(start)}
	if err != nil {
		res.Error = err.Error()
	}
	return res
}

// warmSession делает ровно то, что клиент сделает первым: берёт список
// серверов и просит их инструменты. Путь тот же, поэтому кэш прогревается именно
// для последующих вызовов; refresh НЕ передаётся — он сбросил бы кэш.
func warmSession(ctx context.Context, cs *mcp.ClientSession) (int, error) {
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_servers"})
	if err != nil {
		return 0, err
	}
	if res.IsError {
		return 0, errors.New(toolText(res))
	}
	var state struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if text := toolText(res); text != "" {
		if err := json.Unmarshal([]byte(text), &state); err != nil {
			return 0, fmt.Errorf("list_servers: %w", err)
		}
	}
	names := make([]string, 0, len(state.Servers))
	for _, s := range state.Servers {
		names = append(names, s.Name)
	}
	if len(names) == 0 {
		return 0, nil
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_commands",
		Arguments: map[string]any{"servers": names},
	})
	if err != nil {
		return 0, err
	}
	if res.IsError {
		return 0, errors.New(toolText(res))
	}
	var perServer map[string]json.RawMessage
	if text := toolText(res); text != "" {
		if err := json.Unmarshal([]byte(text), &perServer); err != nil {
			return 0, fmt.Errorf("list_commands: %w", err)
		}
	}
	total := 0
	var down []string
	for name, raw := range perServer {
		var cmds []json.RawMessage
		if err := json.Unmarshal(raw, &cmds); err != nil {
			down = append(down, name)
			continue
		}
		total += len(cmds)
	}
	if len(down) > 0 {
		sort.Strings(down)
		return total, fmt.Errorf("не ответили серверы: %s", strings.Join(down, ", "))
	}
	return total, nil
}

func toolText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

// bearerAuth добавляет токен набора к каждому запросу клиента MCP.
type bearerAuth struct {
	token string
	base  http.RoundTripper
}

func (t *bearerAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// warmToken: --token, --token-file, MCPRIG_TOKEN, иначе ~/.config/mcprig/<set>.token.
func warmToken(o *warmConfig, set string) (string, error) {
	if o.Token != "" {
		return o.Token, nil
	}
	if o.TokenFile != "" {
		tok, err := readToken(o.TokenFile)
		if err != nil {
			return "", fmt.Errorf("токен: %w", err)
		}
		return tok, nil
	}
	if tok := strings.TrimSpace(os.Getenv("MCPRIG_TOKEN")); tok != "" {
		return tok, nil
	}
	path := filepath.Join(mcprigConfigDir(), set+".token")
	tok, err := readToken(path)
	if err == nil {
		return tok, nil
	}
	return "", fmt.Errorf("нет токена набора %q: --token, --token-file, MCPRIG_TOKEN или %s; клиентскую настройку печатает mcprig init", set, path)
}

func readToken(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(body))
	if tok == "" {
		return "", fmt.Errorf("файл %s пуст", path)
	}
	return tok, nil
}

func mcprigConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "mcprig"
	}
	return filepath.Join(dir, "mcprig")
}

// ─── панель ──────────────────────────────────────────────────────────────────

// panelSet — поля /api/sets, нужные прогреву.
type panelSet struct {
	ID        string `json:"id"`
	Port      int    `json:"port"`
	Autostart bool   `json:"autostart"`
	Open      bool   `json:"open"`
}

// panelBaseURL: с --all --url — адрес самой панели; иначе локальная панель на
// MCPRIG_PORT (9091 по умолчанию, как в deploy/mcprig.service).
func panelBaseURL(o *warmConfig) string {
	if o.URL != "" {
		return o.URL
	}
	return "http://127.0.0.1:" + envDefault("MCPRIG_PORT", "9091")
}

func endpointFor(ctx context.Context, o *warmConfig, set string) (string, error) {
	sets, err := fetchPanelSets(ctx, o)
	if err != nil {
		return "", err
	}
	for _, s := range sets {
		if s.ID == set {
			// открытый набор в панели виден как Open: без ключа он всё равно
			// прогревается, поэтому warmEndpoint не считает это ошибкой.
			o.setOpen = s.Open
			return setEndpoint(panelBaseURL(o), s.Port), nil
		}
	}
	return "", fmt.Errorf("набор %q не найден в панели %s", set, panelBaseURL(o))
}

func setEndpoint(panelURL string, port int) string {
	u, err := url.Parse(panelURL)
	if err != nil || u.Hostname() == "" {
		return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/mcp"
	}
	return u.Scheme + "://" + net.JoinHostPort(u.Hostname(), strconv.Itoa(port)) + "/mcp"
}

func fetchPanelSets(ctx context.Context, o *warmConfig) ([]panelSet, error) {
	base := strings.TrimRight(panelBaseURL(o), "/")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	password := o.Password
	if password == "" {
		password = os.Getenv("MCPRIG_PASSWORD")
	}
	if password != "" {
		user := o.User
		if user == "" {
			user = "admin"
		}
		body, _ := json.Marshal(map[string]string{"user": user, "password": password})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/login", strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		panelHeaders(req, o)
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("панель %s: %w", base, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("панель %s: /api/login %d %s", base, res.StatusCode, strings.TrimSpace(string(raw)))
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/sets", nil)
	if err != nil {
		return nil, err
	}
	panelHeaders(req, o)
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("панель %s: %w", base, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("панель %s: /api/sets %d %s", base, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	var sets []panelSet
	if err := json.Unmarshal(raw, &sets); err != nil {
		return nil, fmt.Errorf("панель %s: /api/sets: %w", base, err)
	}
	return sets, nil
}

// panelHeaders — вход через обратный прокси: секрет и имя пользователя.
func panelHeaders(req *http.Request, o *warmConfig) {
	secret := o.Secret
	if secret == "" {
		secret = os.Getenv("MCPRIG_PROXY_SECRET")
	}
	if secret == "" {
		return
	}
	req.Header.Set("x-mcp-proxy-secret", secret)
	user := o.User
	if user == "" {
		user = "admin"
	}
	req.Header.Set(envDefault("MCPRIG_USER_HEADER", "Remote-User"), user)
}

func warmAll(o *warmConfig) []warmResult {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	sets, err := fetchPanelSets(ctx, o)
	cancel()
	if err != nil {
		return []warmResult{{Set: "panel", Error: err.Error()}}
	}
	if len(sets) == 0 {
		return []warmResult{{Set: "panel", Error: "в панели нет наборов"}}
	}
	base := panelBaseURL(o)
	results := make([]warmResult, 0, len(sets))
	for _, s := range sets {
		one := *o
		one.Set = s.ID
		one.setOpen = s.Open
		ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
		results = append(results, warmEndpoint(ctx, &one, s.ID, setEndpoint(base, s.Port)))
		cancel()
	}
	return results
}

// ─── вывод ───────────────────────────────────────────────────────────────────

func renderWarm(results []warmResult) []byte {
	var b strings.Builder
	tools, ms, fails := 0, 0.0, 0
	for _, r := range results {
		if r.Error != "" {
			fails++
			fmt.Fprintf(&b, "%s: ошибка: %s\n", r.Set, r.Error)
			continue
		}
		tools += r.Tools
		ms += r.Ms
		fmt.Fprintf(&b, "%s: %d %s, %.1f мс\n", r.Set, r.Tools, instrumentWord(r.Tools), r.Ms)
	}
	fmt.Fprintf(&b, "итог: %d %s, %d %s, %.1f мс", len(results), setWord(len(results)), tools, instrumentWord(tools), ms)
	if fails > 0 {
		fmt.Fprintf(&b, ", %d %s", fails, errorWord(fails))
	}
	b.WriteByte('\n')
	return []byte(b.String())
}

func elapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

func instrumentWord(n int) string {
	if n%10 == 1 && n%100 != 11 {
		return "инструмент"
	}
	if n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20) {
		return "инструмента"
	}
	return "инструментов"
}

func setWord(n int) string {
	if n%10 == 1 && n%100 != 11 {
		return "набор"
	}
	if n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20) {
		return "набора"
	}
	return "наборов"
}

func errorWord(n int) string {
	if n%10 == 1 && n%100 != 11 {
		return "ошибка"
	}
	if n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20) {
		return "ошибки"
	}
	return "ошибок"
}
