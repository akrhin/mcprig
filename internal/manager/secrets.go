package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Источники значения переменной, по порядку цепочки.
const (
	sourceOpenBao = "openbao"
	sourceFile    = "file"
	sourceEnv     = "env"
)

var errSecretNotFound = errors.New("значение не найдено")

var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envFile — <dataDir>/env.json: плоский объект, только строки, 0600.
type envFile struct {
	mu     sync.Mutex
	path   string
	values map[string]string
}

func loadEnvFile(path string) (*envFile, error) {
	f := &envFile{path: path, values: map[string]string{}}
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("env.json: %w", err)
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			f.values[k] = s
		}
	}
	return f, nil
}

func (f *envFile) get(name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[name]
	return v, ok
}

func (f *envFile) set(name, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[name] = value
	return f.saveLocked()
}

func (f *envFile) delete(name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.values[name]; !ok {
		return false, nil
	}
	delete(f.values, name)
	return true, f.saveLocked()
}

func (f *envFile) saveLocked() error {
	body, err := json.MarshalIndent(f.values, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(f.path, append(body, '\n'), 0o600)
}

// bao — клиент OpenBao KV v2.
type bao struct {
	addr   string
	token  string
	mount  string
	prefix string
	client *http.Client
}

func newBaoFromEnv() *bao {
	addr := envOr(os.Getenv("BAO_ADDR"), os.Getenv("OPENBAO_ADDR"))
	token := envOr(os.Getenv("BAO_TOKEN"), os.Getenv("OPENBAO_TOKEN"))
	if addr == "" || token == "" {
		return nil
	}
	return &bao{
		addr:   strings.TrimRight(addr, "/"),
		token:  token,
		mount:  envOr(os.Getenv("BAO_MOUNT"), "secret"),
		prefix: strings.Trim(envOr(os.Getenv("BAO_PREFIX"), "mcp"), "/"),
		client: newBaoClient(),
	}
}

// newBaoClient — http.Client OpenBao: редирект на другой хост запрещён,
// иначе X-Vault-Token уходит чужому хосту (Go его не вырезает).
func newBaoClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				return errors.New("OpenBao: редирект на другой хост запрещён")
			}
			return nil
		},
	}
}

func (b *bao) dataURL(name string) string {
	p := b.prefix
	if p == "" {
		p = name
	} else {
		p += "/" + name
	}
	return b.addr + "/v1/" + b.mount + "/data/" + p
}

func (b *bao) metaURL(name string) string {
	p := b.prefix
	if p == "" {
		p = name
	} else {
		p += "/" + name
	}
	return b.addr + "/v1/" + b.mount + "/metadata/" + p
}

func (b *bao) do(req *http.Request) ([]byte, error) {
	req.Header.Set("X-Vault-Token", b.token)
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errSecretNotFound
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("OpenBao: %s: %s", resp.Status, truncate(strings.TrimSpace(string(body)), 300))
	}
	return body, nil
}

func (b *bao) get(name string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, b.dataURL(name), nil)
	if err != nil {
		return "", err
	}
	body, err := b.do(req)
	if err != nil {
		return "", err
	}
	var out struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("OpenBao: ответ не JSON: %w", err)
	}
	v, _ := out.Data.Data["value"].(string)
	if v == "" {
		return "", errSecretNotFound
	}
	return v, nil
}

func (b *bao) put(name, value string) error {
	payload, err := json.Marshal(map[string]any{"data": map[string]string{"value": value}})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, b.dataURL(name), strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = b.do(req)
	return err
}

func (b *bao) delete(name string) (bool, error) {
	req, err := http.NewRequest(http.MethodDelete, b.metaURL(name), nil)
	if err != nil {
		return false, err
	}
	_, err = b.do(req)
	if errors.Is(err, errSecretNotFound) {
		return false, nil
	}
	return err == nil, err
}

// secretCacheTTL — сколько Get держит ответ. Переменные спрашивают на каждый
// старт набора и на каждый GET /api/env, а больной OpenBao отдаёт 5 с на запрос.
const secretCacheTTL = 30 * time.Second

type secretEntry struct {
	value   string
	source  string
	expires time.Time
}

// Secrets — цепочка OpenBao → env.json → окружение процесса.
type Secrets struct {
	local     *envFile
	bao       *bao
	mu        sync.Mutex // защищает cache
	cache     map[string]secretEntry
	remoteErr atomic.Pointer[string]
}

func NewSecrets(dataDir string) (*Secrets, error) {
	f, err := loadEnvFile(filepath.Join(dataDir, "env.json"))
	if err != nil {
		return nil, err
	}
	return &Secrets{local: f, bao: newBaoFromEnv()}, nil
}

func (s *Secrets) OpenBao() bool { return s.bao != nil }

func (s *Secrets) setRemoteErr(err error) {
	if err == nil {
		s.remoteErr.Store(nil)
		return
	}
	msg := err.Error()
	s.remoteErr.Store(&msg)
}

func (s *Secrets) RemoteError() string {
	if p := s.remoteErr.Load(); p != nil {
		return *p
	}
	return ""
}

// reservedSecret: переменные самого менеджера. Их нельзя подставлять в
// сервер набора — иначе чужой конфиг унесёт пароль панели или токен OpenBao.
func reservedSecret(name string) bool {
	return strings.HasPrefix(name, "MCPRIG_") ||
		strings.HasPrefix(name, "BAO_") ||
		strings.HasPrefix(name, "OPENBAO_")
}

// Get возвращает первое найденное значение и его источник. Ответ кэшируется
// на secretCacheTTL; пустой ответ тоже — иначе отсутствующая переменная снова
// идёт в сеть на каждом старте набора.
func (s *Secrets) Get(name string) (string, string) {
	if reservedSecret(name) {
		return "", ""
	}
	if v, source, ok := s.cached(name); ok {
		return v, source
	}
	v, source := s.resolve(name)
	s.cachePut(name, secretEntry{value: v, source: source})
	return v, source
}

func (s *Secrets) cached(name string) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[name]
	if !ok || time.Now().After(e.expires) {
		return "", "", false
	}
	return e.value, e.source, true
}

func (s *Secrets) cachePut(name string, e secretEntry) {
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]secretEntry{}
	}
	e.expires = time.Now().Add(secretCacheTTL)
	s.cache[name] = e
	s.mu.Unlock()
}

func (s *Secrets) cacheDrop(name string) {
	s.mu.Lock()
	delete(s.cache, name)
	s.mu.Unlock()
}

func (s *Secrets) resolve(name string) (string, string) {
	if s.bao != nil {
		v, err := s.bao.get(name)
		switch {
		case err == nil:
			s.setRemoteErr(nil)
			return v, sourceOpenBao
		case errors.Is(err, errSecretNotFound):
			s.setRemoteErr(nil)
		default:
			s.setRemoteErr(err)
		}
	}
	if v, ok := s.local.get(name); ok {
		return v, sourceFile
	}
	if v, ok := os.LookupEnv(name); ok {
		return v, sourceEnv
	}
	return "", ""
}

func (s *Secrets) Lookup(name string) (string, bool) {
	v, source := s.Get(name)
	return v, source != ""
}

// Set пишет в явный store или в первый доступный (OpenBao, если настроен).
func (s *Secrets) Set(name, value, store string) (string, error) {
	if !secretNameRe.MatchString(name) {
		return "", fmt.Errorf("переменная %q: имя должно быть вида A-Za-z_", name)
	}
	defer s.cacheDrop(name)
	if store == "" {
		if s.bao != nil {
			store = sourceOpenBao
		} else {
			store = sourceFile
		}
	}
	switch store {
	case sourceOpenBao:
		if s.bao == nil {
			return "", errors.New("OpenBao не настроен: нужны BAO_ADDR и BAO_TOKEN")
		}
		if err := s.bao.put(name, value); err != nil {
			s.setRemoteErr(err)
			return "", err
		}
		s.setRemoteErr(nil)
		return sourceOpenBao, nil
	case sourceFile:
		return sourceFile, s.local.set(name, value)
	default:
		return "", errors.New("store: ожидалось local или openbao")
	}
}

func (s *Secrets) Delete(name, store string) (bool, error) {
	defer s.cacheDrop(name)
	if store == "" {
		if s.bao != nil {
			store = sourceOpenBao
		} else {
			store = sourceFile
		}
	}
	switch store {
	case sourceOpenBao:
		if s.bao == nil {
			return false, errors.New("OpenBao не настроен: нужны BAO_ADDR и BAO_TOKEN")
		}
		removed, err := s.bao.delete(name)
		if err != nil {
			s.setRemoteErr(err)
			return false, err
		}
		s.setRemoteErr(nil)
		return removed, nil
	case sourceFile:
		return s.local.delete(name)
	default:
		return false, errors.New("store: ожидалось local или openbao")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func envOr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
