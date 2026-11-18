package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akrhin/mcprig/internal/atomicfile"
	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/downstream"
	"github.com/akrhin/mcprig/internal/registry"
	"github.com/akrhin/mcprig/internal/safelog"
	"github.com/akrhin/mcprig/internal/safepath"
	"github.com/akrhin/mcprig/internal/stats"
	"github.com/akrhin/mcprig/internal/toollock"
	"github.com/akrhin/mcprig/internal/toolout"
)

// Формат servers.json — вход lazy-mcp `serve`. ${VAR} остаются нераскрытыми:
// подстановку делает процесс набора, у него свои секреты.
type setTransport struct {
	Type      string `json:"type"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Path      string `json:"path"`
	AuthToken string `json:"authToken,omitempty"`
	// AuthTokens — все действующие ключи набора: набор принимает любой из
	// них. authToken остаётся рядом ради прежних клиентов файла.
	AuthTokens   []string `json:"authTokens,omitempty"`
	AllowedHosts []string `json:"allowedHosts,omitempty"`
	// AllowAnonymous marks an open set: the proxy listens without a bearer.
	AllowAnonymous bool `json:"allowAnonymous,omitempty"`
	// ClientNames — подпись ключа по отпечатку: набор берёт её в метку
	// клиента статистики. Отпечаток, а не токен.
	ClientNames map[string]string `json:"clientNames,omitempty"`
	// KeyProfiles — профиль ключа по отпечатку: набор находит им профиль
	// вызывающего. Отпечаток, а не подпись: подпись клиент называет сам.
	KeyProfiles map[string]string `json:"keyProfiles,omitempty"`
	// ToolOutTTL — срок жизни выгрузки усечённого ответа. Маркер обещает файл
	// на этот срок, и панель чистит tool-out по тому же сроку.
	ToolOutTTL string `json:"toolOutTtl,omitempty"`
}

type setHealthMonitor struct {
	Interval int `json:"interval"`
	Timeout  int `json:"timeout"`
}

type setFile struct {
	Servers        []*config.Server `json:"servers"`
	Transport      setTransport     `json:"transport"`
	HealthMonitor  setHealthMonitor `json:"healthMonitor"`
	RequestTimeout int              `json:"requestTimeout"`
	// RateLimit — тот же блок, что в реестре: лимит исполняет сам набор.
	RateLimit *config.RateLimit `json:"rateLimit,omitempty"`
	// Profiles — тот же блок, что в реестре: профиль исполняет сам набор.
	Profiles map[string]*config.Profile `json:"profiles,omitempty"`
}

// ErrNoKey — набор ни закрыт, ни с ключом: собирать конфиг не из чего, старт
// такой набор всё равно отобьёт.
var ErrNoKey = errors.New("нет активного ключа — выдайте новый")

// BuildSetConfig собирает конфиг набора из реестра.
func BuildSetConfig(set *registry.Set, servers []registry.Server, bindIP string, allowedHosts []string) ([]byte, error) {
	if set.Token == "" && !set.Open {
		return nil, fmt.Errorf("у набора %s %w", set.ID, ErrNoKey)
	}
	if bindIP == "" {
		bindIP = "0.0.0.0"
	}
	// Срок жизни выгрузки — тот, по которому панель чистит tool-out: маркер
	// обещает клиенту файл ровно на него, и набор обязан обещать то же.
	toolCfg, _ := toolout.FromEnv()
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
			AuthTokens:     registry.ActiveTokens(set.Keys),
			AllowedHosts:   allowedHosts,
			AllowAnonymous: set.Open && set.Token == "",
			ClientNames:    clientNames(set.Keys),
			KeyProfiles:    keyProfiles(set.Keys),
			ToolOutTTL:     toolCfg.TTL.String(),
		},
		HealthMonitor:  setHealthMonitor{Interval: 60000, Timeout: 120000},
		RequestTimeout: 120000,
		RateLimit:      set.RateLimit,
		Profiles:       set.Profiles,
	}
	for _, name := range set.Servers {
		srv := byName[name]
		if srv == nil {
			return nil, fmt.Errorf("сервера %s больше нет в реестре — набор не пересобран", name)
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

// clientNames — подпись каждого ключа под его отпечатком. Подпись берётся из
// label (прежнее имя — note). Ключ без токена (такой остаётся только в истории
// отзыва) и пустая подпись имени не дают.
func clientNames(keys []registry.Key) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		label := strings.TrimSpace(key.Label)
		if label == "" {
			label = strings.TrimSpace(key.Note)
		}
		if key.Token == "" || label == "" {
			continue
		}
		out[stats.Fingerprint(key.Token)] = label
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// keyProfiles — профиль каждого ключа под его отпечатком. Ключ без профиля или
// без токена (история отзыва) в карту не попадает: набор считает его
// неограниченным по профилю.
func keyProfiles(keys []registry.Key) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		profile := strings.TrimSpace(key.Profile)
		if key.Token == "" || profile == "" {
			continue
		}
		out[stats.Fingerprint(key.Token)] = profile
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// WriteSetConfig пишет <dataDir>/sets/<id>/servers.json атомарно, 0600.
func WriteSetConfig(dataDir, id string, body []byte) (string, error) {
	dir := filepath.Join(dataDir, "sets", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "servers.json")
	if err := writeAtomic(path, body); err != nil {
		return "", err
	}
	pruneLock(dataDir, id, body)
	return path, nil
}

// pruneLock снимает записи карантина у серверов, которых в наборе больше нет:
// файл лежит в каталоге набора, и сам он не уменьшится.
func pruneLock(dataDir, id string, body []byte) {
	var cfg struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return
	}
	keep := make(map[string]bool, len(cfg.Servers))
	for _, srv := range cfg.Servers {
		keep[srv.Name] = true
	}
	_ = toollock.Update(toollock.Path(dataDir, id), func(lock *toollock.File) bool {
		dirty := false
		for key := range lock.Tools {
			if server, _ := toollock.Split(key); !keep[server] {
				delete(lock.Tools, key)
				dirty = true
			}
		}
		return dirty
	})
}

// LogPath — <dataDir>/sets/<id>/mcprig.log.
func LogPath(dataDir, id string) string {
	return filepath.Join(dataDir, "sets", id, "mcprig.log")
}

// ServerLogPath — <dataDir>/sets/<id>/serverlogs/<server>.log: хвост вывода
// процесса сервера, который ведёт сам набор.
func ServerLogPath(dataDir, id, name string) string {
	return downstream.ServerLogPath(filepath.Join(dataDir, "sets", id), name)
}

// clearServerLogs убирает хвосты серверов на первом запуске набора: строки
// прошлого сеанса не должны выглядеть текущими, а автоперезапуск после падения
// дописывает в тот же файл под новой меткой, чтобы трейс не пропадал.
func clearServerLogs(dataDir, id string) error {
	if !safepath.Name(id) {
		return fmt.Errorf("имя набора %q вне шаблона", id)
	}
	return os.RemoveAll(filepath.Join(dataDir, "sets", id, "serverlogs"))
}

// migrateLog переносит унаследованный lazy-mcp.log в mcprig.log один раз: без
// этого хвост старого лога пропал бы из панели после переименования.
func migrateLog(log *slog.Logger, dataDir, id string) {
	dir := filepath.Join(dataDir, "sets", id)
	old := filepath.Join(dir, "lazy-mcp.log")
	if _, err := os.Stat(old); err != nil {
		return
	}
	next := filepath.Join(dir, "mcprig.log")
	if _, err := os.Stat(next); err == nil {
		return
	}
	if err := os.Rename(old, next); err != nil && log != nil {
		log.Warn("лог набора не переименован", "set", safelog.Value(id), "err", err)
	}
}

// writeAtomic делегирует в atomicfile.Write: та же замена через rename, но с
// fsync каталога. Файлы наборов и секретов всегда 0600.
func writeAtomic(path string, body []byte) error {
	return atomicfile.Write(path, body)
}

// addRefs собирает имена ${VAR} из строки в seen.
func addRefs(seen map[string]bool, text string) {
	for _, name := range config.EnvRefs(text) {
		seen[name] = true
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

// fileRefs — {file:…} в env или заголовке: значение читается с диска и уходит
// в процесс набора или на URL. Импортированный конфиг так уносит env.json с
// ключами всех наборов, поэтому такая ссылка отклоняется, а не разрешается.
// Шаблон ищется в любом месте строки: раскрыватель config матчит его не только
// в начале, и «p{file:…}» иначе проходит проверку.
func fileRefs(s *config.Server) (string, bool) {
	scan := func(m map[string]string) (string, bool) {
		for key, value := range m {
			if config.HasFileRef(value) {
				return key, true
			}
		}
		return "", false
	}
	if key, ok := scan(s.Env); ok {
		return key, true
	}
	return scan(s.Headers)
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
	// Параллельно: последовательный обход даёт 5 с на ссылку больного OpenBao.
	vals := map[string]secretValue{}
	if sec != nil {
		names := serverEnvRefs(srv)
		for i, v := range sec.Resolve(names) {
			vals[names[i]] = v
		}
	}
	lookup := func(name string) (string, bool) {
		v := vals[name]
		return v.value, v.source != ""
	}
	expand := func(text string) string {
		return config.ExpandEnvRefs(text, func(name string) (string, bool) {
			v, ok := lookup(name)
			return v, ok
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
