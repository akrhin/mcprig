package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/registry"
)

// Формат servers.json — вход lazy-mcp `serve`. ${VAR} остаются нераскрытыми:
// подстановку делает процесс набора, у него свои секреты.
type setTransport struct {
	Type         string   `json:"type"`
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	Path         string   `json:"path"`
	AuthToken    string   `json:"authToken,omitempty"`
	AllowedHosts []string `json:"allowedHosts,omitempty"`
	// AllowAnonymous marks an open set: the proxy listens without a bearer.
	AllowAnonymous bool `json:"allowAnonymous,omitempty"`
}

type setHealthMonitor struct {
	Interval int `json:"interval"`
	Timeout  int `json:"timeout"`
}

type setFile struct {
	Servers        []*config.Server `json:"servers"`
	Transport      setTransport     `json:"transport"`
	Logging        map[string]any   `json:"logging,omitempty"`
	HealthMonitor  setHealthMonitor `json:"healthMonitor"`
	RequestTimeout int              `json:"requestTimeout"`
}

// BuildSetConfig собирает конфиг набора из реестра.
func BuildSetConfig(set *registry.Set, servers []registry.Server, bindIP string) ([]byte, error) {
	if set.Token == "" && !set.Open {
		return nil, fmt.Errorf("у набора %s нет активного ключа — выдайте новый", set.ID)
	}
	if bindIP == "" {
		bindIP = "0.0.0.0"
	}
	byName := make(map[string]*config.Server, len(servers))
	for _, s := range servers {
		if s != nil {
			byName[s.Name] = s
		}
	}
	out := setFile{
		Servers: make([]*config.Server, 0, len(set.Servers)),
		Transport: setTransport{
			Type:           "http",
			Host:           bindIP,
			Port:           set.Port,
			Path:           "/mcp",
			AuthToken:      set.Token,
			AllowAnonymous: set.Open && set.Token == "",
		},
		Logging:        map[string]any{"level": "info"},
		HealthMonitor:  setHealthMonitor{Interval: 60000, Timeout: 120000},
		RequestTimeout: 120000,
	}
	for _, name := range set.Servers {
		srv := byName[name]
		if srv == nil {
			return nil, fmt.Errorf("сервер %s исчез из реестра", name)
		}
		cp := *srv
		out.Servers = append(out.Servers, &cp)
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// WriteSetConfig пишет <dataDir>/sets/<id>/servers.json атомарно, 0600.
func WriteSetConfig(dataDir, id string, body []byte) (string, error) {
	dir := filepath.Join(dataDir, "sets", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "servers.json")
	if err := writeAtomic(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// LogPath — <dataDir>/sets/<id>/lazy-mcp.log.
func LogPath(dataDir, id string) string {
	return filepath.Join(dataDir, "sets", id, "lazy-mcp.log")
}

func writeAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var envRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// addRefs собирает имена ${VAR} из строки в seen.
func addRefs(seen map[string]bool, text string) {
	for _, m := range envRefRe.FindAllStringSubmatch(text, -1) {
		seen[m[1]] = true
	}
}

// serverEnvRefs — имена ${VAR}, на которые ссылается сервер: env, headers,
// url, cwd, command[]. Отсортированы без повторов.
func serverEnvRefs(s *config.Server) []string {
	seen := map[string]bool{}
	if s != nil {
		for name, value := range s.Env {
			addRefs(seen, name)
			addRefs(seen, value)
		}
		for name, value := range s.Headers {
			addRefs(seen, name)
			addRefs(seen, value)
		}
		addRefs(seen, s.URL)
		addRefs(seen, s.Cwd)
		for _, part := range s.Command {
			addRefs(seen, part)
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// envRefs — имена ${VAR} всех серверов набора, без повторов.
func envRefs(servers []registry.Server) []string {
	seen := map[string]bool{}
	for _, s := range servers {
		for _, name := range serverEnvRefs(s) {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// reservedRefs — служебные ${VAR} сервера, которые нельзя раскрывать: иначе
// импортированный конфиг уносит секрет панели в env набора или в URL.
func reservedRefs(s *config.Server) []string {
	out := []string{}
	for _, name := range serverEnvRefs(s) {
		if reservedSecret(name) {
			out = append(out, name)
		}
	}
	return out
}

// setServers — серверы набора в его порядке.
func setServers(set *registry.Set, servers []registry.Server) []registry.Server {
	byName := make(map[string]registry.Server, len(servers))
	for _, s := range servers {
		if s != nil {
			byName[s.Name] = s
		}
	}
	out := make([]registry.Server, 0, len(set.Servers))
	for _, name := range set.Servers {
		if s, ok := byName[name]; ok {
			out = append(out, s)
		}
	}
	return out
}

// expandServer копирует сервер, раскрыв ${VAR} из цепочки секретов: менеджеру
// это нужно для probe и browse, сам набор раскрывает переменные у себя.
func expandServer(srv *config.Server, sec *Secrets) *config.Server {
	lookup := func(name string) (string, bool) {
		if sec == nil {
			return "", false
		}
		return sec.Lookup(name)
	}
	expand := func(text string) string {
		return envRefRe.ReplaceAllStringFunc(text, func(m string) string {
			name := envRefRe.FindStringSubmatch(m)[1]
			if v, ok := lookup(name); ok {
				return v
			}
			return m
		})
	}
	cp := *srv
	if len(srv.Command) > 0 {
		cmd := make([]string, len(srv.Command))
		for i, part := range srv.Command {
			cmd[i] = expand(part)
		}
		cp.Command = cmd
	}
	cp.Cwd = expand(srv.Cwd)
	cp.URL = expand(srv.URL)
	if len(srv.Env) > 0 {
		cp.Env = make(map[string]string, len(srv.Env))
		for k, v := range srv.Env {
			cp.Env[expand(k)] = expand(v)
		}
	}
	if len(srv.Headers) > 0 {
		cp.Headers = make(map[string]string, len(srv.Headers))
		for k, v := range srv.Headers {
			cp.Headers[expand(k)] = expand(v)
		}
	}
	return &cp
}

func mentions(line, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(line[i:], name)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(name)
		beforeOK := j == 0 || !wordByte(line[j-1])
		afterOK := end == len(line) || !wordByte(line[end])
		if beforeOK && afterOK {
			return true
		}
		i = j + 1
	}
}

func wordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '.'
}
