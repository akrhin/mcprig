// Package config reads the lazy-mcp `servers.json` format. The format is a
// contract with the sets this binary spawns, so it stays byte-compatible with
// what lazy-mcp already accepts.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type TransportKind string

const (
	TransportStdio TransportKind = "stdio"
	TransportHTTP  TransportKind = "http"
)

// Server is one downstream MCP server. Validation never fails the load: an
// unusable entry keeps its ConfigError and shows up unhealthy, the rest works.
type Server struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Transport   TransportKind     `json:"transport,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	// EnvAllow — имена переменных окружения владельца, которые сервер получает
	// вдобавок к whitelist. Больше ничего из os.Environ() вниз не уходит.
	EnvAllow    []string          `json:"envAllow,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Enabled     bool              `json:"enabled"`
	Permissions *Permissions      `json:"permissions,omitempty"`
	OAuth       *OAuth            `json:"oauth,omitempty"`
	Sandbox     *Sandbox          `json:"sandbox,omitempty"`
	ConfigError string            `json:"_configError,omitempty"`
}

// Sandbox — изоляция stdio-сервера. Пустой блок означает умолчания: bwrap,
// сеть выключена, при недоступной песочнице сервер не стартует.
type Sandbox struct {
	Mode          string   `json:"mode,omitempty"`          // none | bwrap
	OnUnavailable string   `json:"onUnavailable,omitempty"` // deny | warn | allow
	RO            []string `json:"ro,omitempty"`
	RW            []string `json:"rw,omitempty"`
	Network       string   `json:"network,omitempty"` // none | allow
	MemoryMb      int      `json:"memoryMb,omitempty"`
	Pids          int      `json:"pids,omitempty"`
	TimeoutSec    int      `json:"timeoutSec,omitempty"`
	// TmpMb — размер tmpfs /tmp и заодно RLIMIT_FSIZE: иначе сервер набивает
	// tmpfs до лимита хоста.
	TmpMb int `json:"tmpMb,omitempty"`
}

const (
	SandboxModeNone  = "none"
	SandboxModeBwrap = "bwrap"

	OnUnavailableDeny  = "deny"
	OnUnavailableWarn  = "warn"
	OnUnavailableAllow = "allow"

	SandboxNetworkNone  = "none"
	SandboxNetworkAllow = "allow"

	// DefaultSandboxMemoryMb ограничивает виртуальную память (RLIMIT_AS), а не
	// RSS: под 512 МиБ не поднимается ни node, ни Go (проверено живьём —
	// canary на go-sdk падал «failed to reserve page summary memory»).
	DefaultSandboxMemoryMb = 2048
	DefaultSandboxPids     = 256
	DefaultSandboxTimeout  = 30
	// DefaultSandboxTmpMb — /tmp внутри песочницы: 128 МиБ хватает node и
	// python, а диск хоста не забивается.
	DefaultSandboxTmpMb = 128
)

// DefaultSandboxRO — минимум, без которого не живёт ни один интерпретатор.
// Несуществующие пути Wrap пропускает: bwrap падает на отсутствующем источнике.
var DefaultSandboxRO = []string{"/usr", "/bin", "/lib", "/lib64", "/etc/ssl", "/etc/resolv.conf", "/etc/passwd", "/etc/group"}

// setDefaults заполняет незаданные поля умолчаниями.
func (s *Sandbox) setDefaults() {
	if s.Mode == "" {
		s.Mode = SandboxModeBwrap
	}
	if s.OnUnavailable == "" {
		s.OnUnavailable = OnUnavailableDeny
	}
	if s.Network == "" {
		s.Network = SandboxNetworkNone
	}
	if len(s.RO) == 0 {
		s.RO = append([]string(nil), DefaultSandboxRO...)
	}
	if s.MemoryMb == 0 {
		s.MemoryMb = DefaultSandboxMemoryMb
	}
	if s.Pids == 0 {
		s.Pids = DefaultSandboxPids
	}
	if s.TimeoutSec == 0 {
		s.TimeoutSec = DefaultSandboxTimeout
	}
	if s.TmpMb == 0 {
		s.TmpMb = DefaultSandboxTmpMb
	}
}

// validate rejects a block whose values would become bwrap flags as-is: argv
// is an attack surface here (CVE-2026-55887), so paths must be absolute and
// every enum must be known.
func (s *Sandbox) validate() string {
	switch s.Mode {
	case SandboxModeNone, SandboxModeBwrap:
	default:
		return fmt.Sprintf("sandbox.mode: %q, допустимо none|bwrap", s.Mode)
	}
	switch s.OnUnavailable {
	case OnUnavailableDeny, OnUnavailableWarn, OnUnavailableAllow:
	default:
		return fmt.Sprintf("sandbox.onUnavailable: %q, допустимо deny|warn|allow", s.OnUnavailable)
	}
	switch s.Network {
	case SandboxNetworkNone, SandboxNetworkAllow:
	default:
		return fmt.Sprintf("sandbox.network: %q, допустимо none|allow", s.Network)
	}
	for _, field := range []struct {
		name  string
		paths []string
	}{{"ro", s.RO}, {"rw", s.RW}} {
		for _, p := range field.paths {
			if !filepath.IsAbs(p) || strings.HasPrefix(p, "-") {
				return fmt.Sprintf("sandbox.%s: %q — нужен абсолютный путь", field.name, p)
			}
		}
	}
	if s.MemoryMb < 0 || s.Pids < 0 || s.TimeoutSec < 0 || s.TmpMb < 0 {
		return "sandbox: memoryMb, pids, tmpMb и timeoutSec не могут быть отрицательными"
	}
	return ""
}

// CallTimeout — свой предел одного вызова инструмента; 0 — действует
// requestTimeout набора.
func (s *Server) CallTimeout() time.Duration {
	if s.Sandbox == nil || s.Sandbox.Mode == SandboxModeNone || s.Sandbox.TimeoutSec <= 0 {
		return 0
	}
	return time.Duration(s.Sandbox.TimeoutSec) * time.Second
}

// Warnings — некритичные замечания: npx/uvx без пина версии ставят «сегодняшний
// latest», а не тот код, который проверяли.
func (s *Server) Warnings() []string {
	var out []string
	if sb := s.Sandbox; sb != nil && sb.Mode == SandboxModeBwrap {
		// RLIMIT_AS — виртуальная память: node падает даже на 1 ГиБ.
		if sb.MemoryMb > 0 && sb.MemoryMb < 2048 && isNodeCommand(s.Command) {
			out = append(out, fmt.Sprintf("sandbox.memoryMb=%d: node/npx не поднимется, нужно ≥2048", sb.MemoryMb))
		}
	}
	if sb := s.Sandbox; sb != nil && sb.Mode == SandboxModeNone &&
		(sb.MemoryMb > 0 || sb.Pids > 0 || sb.TmpMb > 0 || sb.TimeoutSec > 0 || len(sb.RO) > 0 || len(sb.RW) > 0) {
		out = append(out, "sandbox.mode=none: лимиты и монтирования из блока не применяются")
	}
	for _, name := range s.EnvAllow {
		if _, ok := s.Env[name]; ok {
			continue
		}
		if _, ok := os.LookupEnv(name); !ok {
			out = append(out, fmt.Sprintf("envAllow: %s не задана в окружении — сервер получит пусто", name))
		}
	}
	if len(s.Command) == 0 {
		return out
	}
	switch filepath.Base(s.Command[0]) {
	case "npx", "uvx":
		pinned := false
		for _, a := range s.Command[1:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if strings.Contains(strings.TrimPrefix(a, "@"), "@") || strings.Contains(a, "==") {
				pinned = true
			}
			break
		}
		if !pinned {
			out = append(out, fmt.Sprintf("%s без пина версии: код подменяется молча", filepath.Base(s.Command[0])))
		}
	}
	return out
}

// hasAnyPath — есть ли в списке хотя бы один из путей.
func hasAnyPath(paths []string, want ...string) bool {
	for _, p := range paths {
		for _, w := range want {
			if p == w {
				return true
			}
		}
	}
	return false
}

// isNodeCommand — команда, которой нужен запас виртуальной памяти.
func isNodeCommand(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch filepath.Base(argv[0]) {
	case "node", "npx", "npm", "pnpm", "yarn", "bun":
		return true
	}
	return false
}

// untrusted требует песочницы: сервер без неё может выгрести ключи хоста,
// а тег ставится именно на недоверенные серверы.
func (s *Server) untrusted() bool {
	for _, tag := range s.Tags {
		if strings.EqualFold(tag, "untrusted") {
			return true
		}
	}
	return false
}

// OAuth tunes the client-side authorization code flow for one server. An empty
// block is fine: the flow starts only when the server answers 401 or 403.
type OAuth struct {
	Disabled     bool     `json:"disabled,omitempty"`
	CallbackPort int      `json:"callbackPort,omitempty"` // loopback, default 8947
	RedirectURL  string   `json:"redirectUrl,omitempty"`
	ClientID     string   `json:"clientId,omitempty"`
	ClientSecret string   `json:"clientSecret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

// DefaultCallbackPort is the loopback port the redirect listener uses unless a
// config overrides it. Fixed on purpose: some authorization servers only accept
// a redirect URI registered by hand.
const DefaultCallbackPort = 8947

// validate returns a defect description, empty when the block is usable.
func (o *OAuth) validate(prefix string) string {
	if o == nil {
		return ""
	}
	if o.CallbackPort < 0 || o.CallbackPort > 65535 {
		return fmt.Sprintf("%s.callbackPort: ожидался порт 0–65535, получено %d", prefix, o.CallbackPort)
	}
	if o.ClientSecret != "" && o.ClientID == "" {
		return prefix + ".clientSecret без clientId"
	}
	return ""
}

// merge lays one server's block over the set-wide one.
func (o *OAuth) merge(global *OAuth) *OAuth {
	out := &OAuth{}
	for _, src := range []*OAuth{global, o} {
		if src == nil {
			continue
		}
		if src.Disabled {
			out.Disabled = true
		}
		if src.CallbackPort != 0 {
			out.CallbackPort = src.CallbackPort
		}
		if src.RedirectURL != "" {
			out.RedirectURL = src.RedirectURL
		}
		if src.ClientID != "" {
			out.ClientID = src.ClientID
		}
		if src.ClientSecret != "" {
			out.ClientSecret = src.ClientSecret
		}
		if len(src.Scopes) > 0 {
			out.Scopes = src.Scopes
		}
	}
	if out.CallbackPort == 0 {
		out.CallbackPort = DefaultCallbackPort
	}
	if out.RedirectURL == "" {
		out.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", out.CallbackPort)
	}
	return out
}

// PermissionAction is allow or deny; lazy-mcp's "ask" is not implemented.
type PermissionAction string

const (
	PermissionAllow PermissionAction = "allow"
	PermissionDeny  PermissionAction = "deny"
)

// Permissions is the experimental allow/deny block, on the top level or one
// server. Without it everything is allowed.
type Permissions struct {
	Default PermissionAction `json:"default,omitempty"`
	Rules   []Rule           `json:"rules,omitempty"`
}

// Rule matches the command name by glob; Server is only allowed on global
// rules and matches the server name (default "*").
type Rule struct {
	Server  string           `json:"server,omitempty"`
	Pattern string           `json:"pattern"`
	Action  PermissionAction `json:"action"`
	Message string           `json:"message,omitempty"`
}

// validate returns a defect description, empty when the block is usable.
func (p *Permissions) validate(prefix string, global bool) string {
	if p == nil {
		return ""
	}
	switch p.Default {
	case "", PermissionAllow, PermissionDeny:
	default:
		return fmt.Sprintf("%s.default: ожидалось allow или deny, получено %q", prefix, p.Default)
	}
	for i, r := range p.Rules {
		rp := fmt.Sprintf("%s.rules[%d]", prefix, i)
		if r.Pattern == "" {
			return rp + ".pattern: пустой шаблон"
		}
		switch r.Action {
		case PermissionAllow, PermissionDeny:
		default:
			return fmt.Sprintf("%s.action: ожидалось allow или deny, получено %q", rp, r.Action)
		}
		if !global && r.Server != "" {
			return rp + ".server допустим только в глобальном блоке"
		}
	}
	return ""
}

// UnmarshalJSON accepts both command forms: the array form and the legacy
// `"command": "npx"` plus `"args": […]`.
func (s *Server) UnmarshalJSON(data []byte) error {
	type raw struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Transport   string          `json:"transport"`
		Command     json.RawMessage `json:"command"`
		Args        []string        `json:"args"`
		Cwd         string          `json:"cwd"`
		Env         map[string]any  `json:"env"`
		URL         string          `json:"url"`
		Headers     map[string]any  `json:"headers"`
		Tags        []string        `json:"tags"`
		Enabled     *bool           `json:"enabled"`
		Permissions json.RawMessage `json:"permissions"`
		OAuth       *OAuth          `json:"oauth"`
		Sandbox     json.RawMessage `json:"sandbox"`
		EnvAllow    []string        `json:"envAllow"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	s.Name, s.Description, s.Cwd, s.URL, s.Tags = r.Name, r.Description, r.Cwd, r.URL, r.Tags
	s.Transport = TransportKind(r.Transport)
	s.Enabled = r.Enabled == nil || *r.Enabled
	if len(r.Command) > 0 {
		var argv []string
		if err := json.Unmarshal(r.Command, &argv); err == nil {
			s.Command = argv
		} else {
			var one string
			if err := json.Unmarshal(r.Command, &one); err != nil {
				return fmt.Errorf("command: ожидалась строка или массив строк")
			}
			s.Command = append([]string{one}, r.Args...)
		}
	}
	s.Env = stringMap(r.Env, &s.ConfigError, "env")
	s.EnvAllow = r.EnvAllow
	s.Headers = stringMap(r.Headers, &s.ConfigError, "headers")
	s.decodeSandbox(r.Sandbox)
	s.decodePermissions(r.Permissions)
	if msg := r.OAuth.validate("oauth"); msg != "" {
		if s.ConfigError == "" {
			s.ConfigError = msg
		}
	} else if r.OAuth != nil {
		s.OAuth = r.OAuth
	}
	return nil
}

// decodePermissions soft-fails a broken block onto the server, like lazy-mcp.
func (s *Server) decodePermissions(data json.RawMessage) {
	if len(data) == 0 || string(data) == "null" {
		return
	}
	var p Permissions
	if err := json.Unmarshal(data, &p); err != nil {
		if s.ConfigError == "" {
			s.ConfigError = "permissions: " + err.Error()
		}
		return
	}
	if msg := p.validate("permissions", false); msg != "" {
		if s.ConfigError == "" {
			s.ConfigError = msg
		}
		return
	}
	s.Permissions = &p
}

// decodeSandbox soft-fails: a typo in the block must not brick the whole file.
func (s *Server) decodeSandbox(data json.RawMessage) {
	if len(data) == 0 || string(data) == "null" {
		return
	}
	var sb Sandbox
	if err := json.Unmarshal(data, &sb); err != nil {
		if s.ConfigError == "" {
			s.ConfigError = "sandbox: " + err.Error()
		}
		return
	}
	// Опечатка в имени поля иначе молча даёт умолчание: "memoryMB" или
	// "networkk" выглядели бы как «настроено», а сервер шёл бы с дефолтами.
	if unknown := UnknownSandboxKeys(data); len(unknown) > 0 {
		if s.ConfigError == "" {
			s.ConfigError = "sandbox: неизвестные поля: " + strings.Join(unknown, ", ")
		}
		return
	}
	s.Sandbox = &sb
}

// sandboxFields — единственные имена, которые понимает Sandbox.
var sandboxFields = map[string]bool{
	"mode": true, "onUnavailable": true, "ro": true, "rw": true,
	"network": true, "memoryMb": true, "pids": true, "timeoutSec": true, "tmpMb": true,
}

// UnknownSandboxKeys возвращает имена полей sandbox, которых нет в схеме.
func UnknownSandboxKeys(data json.RawMessage) []string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	var out []string
	for k := range raw {
		if !sandboxFields[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func stringMap(in map[string]any, configErr *string, field string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		str, ok := v.(string)
		if !ok {
			if *configErr == "" {
				*configErr = fmt.Sprintf("%s.%s: ожидалась строка", field, k)
			}
			continue
		}
		out[k] = str
	}
	return out
}

var (
	varRefPattern  = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	fileRefPattern = regexp.MustCompile(`\{file:([^}]+)\}`)
)

// expandSecrets resolves the two reference forms the TS manager supports:
// `{file:/abs/path}` reads a secret file (one trailing newline stripped),
// `${VAR}` takes the variable from the process environment. A relative secret
// path or an unset variable is an error; the caller turns it into a ConfigError.
func expandSecrets(value string) (string, error) {
	var firstErr error
	out := fileRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		if firstErr != nil {
			return match
		}
		path := strings.TrimSpace(match[len("{file:") : len(match)-1])
		if !filepath.IsAbs(path) {
			firstErr = fmt.Errorf("{file:%s}: нужен абсолютный путь", path)
			return match
		}
		data, err := os.ReadFile(path)
		if err != nil {
			firstErr = fmt.Errorf("{file:%s}: %v", path, err)
			return match
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	})
	if firstErr != nil {
		return "", firstErr
	}
	out = varRefPattern.ReplaceAllStringFunc(out, func(match string) string {
		if firstErr != nil {
			return match
		}
		name := match[2 : len(match)-1]
		val, ok := os.LookupEnv(name)
		if !ok {
			firstErr = fmt.Errorf("переменная %s не задана", name)
			return match
		}
		return val
	})
	return out, firstErr
}

// expandSecrets resolves references in env and headers; a failure is a
// soft ConfigError of this server, like every other validation defect.
func (s *Server) expandSecrets() {
	expandMap := func(field string, m map[string]string) {
		for key, value := range m {
			out, err := expandSecrets(value)
			if err != nil {
				if s.ConfigError == "" {
					s.ConfigError = fmt.Sprintf("%s.%s: %v", field, key, err)
				}
				continue
			}
			m[key] = out
		}
	}
	expandMap("env", s.Env)
	expandMap("headers", s.Headers)
}

// validate fills ConfigError. Returns false when the entry is unusable.
func (s *Server) validate() bool {
	if s.ConfigError != "" {
		return false
	}
	return s.validateSandbox(s.Sandbox)
}

// validateSandbox — те же правила над явно переданным блоком песочницы:
// Check проверяет копию, не трогая общий объект реестра.
func (s *Server) validateSandbox(sandbox *Sandbox) bool {
	if s.Name == "" {
		s.ConfigError = "нет имени"
		return false
	}
	switch s.Transport {
	case "":
		switch {
		case s.URL != "":
			s.Transport = TransportHTTP
		case len(s.Command) > 0:
			s.Transport = TransportStdio
		default:
			s.ConfigError = "не удалось определить тип: ни url, ни command"
			return false
		}
	case TransportStdio:
		if len(s.Command) == 0 {
			s.ConfigError = "stdio требует command"
			return false
		}
		if s.URL != "" {
			s.ConfigError = "stdio не может иметь url"
			return false
		}
	case TransportHTTP:
		if s.URL == "" {
			s.ConfigError = "http требует url"
			return false
		}
		if len(s.Command) > 0 {
			s.ConfigError = "http не может иметь command"
			return false
		}
	default:
		s.ConfigError = fmt.Sprintf("неизвестный transport %q", s.Transport)
		return false
	}
	if s.Cwd != "" && s.Transport != TransportStdio {
		s.ConfigError = "cwd допустим только для stdio"
		return false
	}
	if sandbox != nil {
		if s.Transport != TransportStdio {
			s.ConfigError = "sandbox допустим только для stdio"
			return false
		}
		sandbox.setDefaults()
		if msg := sandbox.validate(); msg != "" {
			s.ConfigError = msg
			return false
		}
	}
	if s.untrusted() && (sandbox == nil || sandbox.Mode == SandboxModeNone) {
		s.ConfigError = "тег untrusted требует sandbox: сервер без изоляции не стартует"
		return false
	}
	// warn/allow разрешают старт без изоляции, когда bwrap недоступен, — для
	// untrusted это тот же запуск без песочницы, только тише.
	if s.untrusted() && sandbox.OnUnavailable != OnUnavailableDeny {
		s.ConfigError = fmt.Sprintf("тег untrusted требует sandbox.onUnavailable=deny, задано %q", sandbox.OnUnavailable)
		return false
	}
	return true
}

// Check прогоняет те же правила, что Load, над сервером, собранным снаружи
// файла: реестр и кнопки probe/browse запускают ровно то, что набор отверг бы.
// Работает на копии: стор отдаёт общий объект, а setDefaults и ConfigError —
// запись в него. Пустая строка — сервер годен.
func (s *Server) Check() string {
	tmp := *s
	tmp.ConfigError = ""
	if s.Sandbox != nil {
		cp := *s.Sandbox
		tmp.Sandbox = &cp
	}
	if tmp.validateSandbox(tmp.Sandbox) {
		return ""
	}
	return tmp.ConfigError
}

// HTTPTransport is the listener the set exposes to MCP clients.
type HTTPTransport struct {
	Type      string `json:"type,omitempty"`
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
	Path      string `json:"path,omitempty"`
	AuthToken string `json:"authToken,omitempty"`
	// AllowAnonymous lets the listener start without an authToken and accepts
	// requests carrying no Authorization header; a presented token is still
	// checked. Set only for an open set.
	AllowAnonymous bool     `json:"allowAnonymous,omitempty"`
	AllowedHosts   []string `json:"allowedHosts,omitempty"`
}

type HealthMonitor struct {
	Interval int `json:"interval,omitempty"` // ms
	Timeout  int `json:"timeout,omitempty"`  // ms
}

// Config is one set's file: the servers it proxies plus its listener.
type Config struct {
	Servers        []*Server      `json:"servers"`
	Transport      *HTTPTransport `json:"transport,omitempty"`
	HealthMonitor  *HealthMonitor `json:"healthMonitor,omitempty"`
	RequestTimeout int            `json:"requestTimeout,omitempty"` // ms
	Permissions    *Permissions   `json:"permissions,omitempty"`
	OAuth          *OAuth         `json:"oauth,omitempty"`
}

// OAuthFor returns the effective OAuth settings for one server, set-wide block
// under the server's own, defaults filled in.
func (c *Config) OAuthFor(srv *Server) *OAuth {
	var own *OAuth
	if srv != nil {
		own = srv.OAuth
	}
	return own.merge(c.OAuth)
}

// Load reads a config file. A server that cannot be identified (no name) fails
// the whole load; every other defect is soft-failed onto that server.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var shape struct {
		Servers        []json.RawMessage `json:"servers"`
		Transport      *HTTPTransport    `json:"transport"`
		HealthMonitor  *HealthMonitor    `json:"healthMonitor"`
		RequestTimeout int               `json:"requestTimeout"`
		Permissions    json.RawMessage   `json:"permissions"`
		OAuth          *OAuth            `json:"oauth"`
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if shape.Transport != nil && shape.Transport.AuthToken != "" {
		token, err := expandSecrets(shape.Transport.AuthToken)
		if err != nil {
			return nil, fmt.Errorf("%s: transport.authToken: %w", path, err)
		}
		shape.Transport.AuthToken = token
	}
	cfg := &Config{Transport: shape.Transport, HealthMonitor: shape.HealthMonitor, RequestTimeout: shape.RequestTimeout}
	// A broken global OAuth block is dropped too: a typo must not brick the set.
	if shape.OAuth.validate("oauth") == "" {
		cfg.OAuth = shape.OAuth
	}
	// A broken global block is dropped, not fatal: a typo must not brick the set.
	if p := decodeGlobalPermissions(shape.Permissions); p != nil {
		cfg.Permissions = p
	}
	seen := make(map[string]bool, len(shape.Servers))
	for i, raw := range shape.Servers {
		srv := &Server{}
		if err := json.Unmarshal(raw, srv); err != nil {
			return nil, fmt.Errorf("%s: сервер #%d: %w", path, i+1, err)
		}
		if srv.Name == "" {
			return nil, fmt.Errorf("%s: сервер #%d без имени", path, i+1)
		}
		if seen[srv.Name] {
			srv.ConfigError = "дубль имени"
		}
		seen[srv.Name] = true
		srv.expandSecrets()
		srv.validate()
		cfg.Servers = append(cfg.Servers, srv)
	}
	return cfg, nil
}

// decodeGlobalPermissions returns nil for an absent or invalid block: the set
// keeps working with everything allowed.
func decodeGlobalPermissions(data json.RawMessage) *Permissions {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var p Permissions
	if err := json.Unmarshal(data, &p); err != nil {
		return nil
	}
	if p.validate("permissions", true) != "" {
		return nil
	}
	return &p
}

// Usable reports whether the set can be served: at least one healthy server.
func (c *Config) Usable() []*Server {
	out := make([]*Server, 0, len(c.Servers))
	for _, s := range c.Servers {
		if s.ConfigError == "" && s.Enabled {
			out = append(out, s)
		}
	}
	return out
}
