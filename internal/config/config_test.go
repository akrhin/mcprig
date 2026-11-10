package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadNormalizes(t *testing.T) {
	path := write(t, `{
	  "servers": [
	    {"name": "remote", "url": "https://example.test/mcp"},
	    {"name": "array", "command": ["npx", "-y", "pkg"]},
	    {"name": "legacy", "command": "uvx", "args": ["srv"]},
	    {"name": "off", "url": "https://x/mcp", "enabled": false}
	  ],
	  "transport": {"type": "http", "port": 9100, "authToken": "t"},
	  "requestTimeout": 120000
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Servers) != 4 {
		t.Fatalf("servers: want 4, got %d", len(cfg.Servers))
	}
	byName := map[string]*Server{}
	for _, s := range cfg.Servers {
		byName[s.Name] = s
	}
	if got := byName["remote"].Transport; got != TransportHTTP {
		t.Errorf("remote transport: want http (from url), got %q", got)
	}
	if got := byName["array"].Transport; got != TransportStdio {
		t.Errorf("array transport: want stdio (from command), got %q", got)
	}
	if got := byName["legacy"].Command; len(got) != 2 || got[0] != "uvx" || got[1] != "srv" {
		t.Errorf("legacy command: want [uvx srv], got %v", got)
	}
	if !byName["remote"].Enabled {
		t.Error("enabled must default to true")
	}
	if byName["off"].Enabled {
		t.Error("enabled:false must be kept")
	}
	if cfg.Transport.Port != 9100 || cfg.RequestTimeout != 120000 {
		t.Errorf("transport/requestTimeout not read: %+v %d", cfg.Transport, cfg.RequestTimeout)
	}
	if usable := cfg.Usable(); len(usable) != 3 {
		t.Errorf("Usable: want 3 (one disabled), got %d", len(usable))
	}
}

func TestSoftFail(t *testing.T) {
	path := write(t, `{
	  "servers": [
	    {"name": "bad-env", "command": ["x"], "env": {"A": 1}},
	    {"name": "no-cmd", "transport": "stdio"},
	    {"name": "no-url", "transport": "http"},
	    {"name": "weird", "transport": "grpc", "url": "https://x"},
	    {"name": "cwd-http", "url": "https://x", "cwd": "/tmp"},
	    {"name": "dup", "command": ["x"]},
	    {"name": "dup", "command": ["y"]}
	  ]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("soft failures must not fail the load: %v", err)
	}
	for i, s := range cfg.Servers {
		want := s.Name != "dup" || i == 6
		if want && s.ConfigError == "" {
			t.Errorf("%s (index %d): want a config error, got none", s.Name, i)
		}
	}
	if cfg.Servers[5].ConfigError != "" || cfg.Servers[6].ConfigError == "" {
		t.Error("duplicate name: only the second occurrence must carry a config error")
	}
	// Только первая запись "dup" валидна — остальные сломаны тем или иным способом.
	if len(cfg.Usable()) != 1 {
		t.Errorf("Usable: want 1 healthy, got %d", len(cfg.Usable()))
	}
}

func TestPermissions(t *testing.T) {
	path := write(t, `{
	  "permissions": {
	    "default": "allow",
	    "rules": [{"server": "a*", "pattern": "write*", "action": "deny", "message": "read only"}]
	  },
	  "servers": [
	    {"name": "a", "command": ["x"], "permissions": {
	      "default": "deny",
	      "rules": [{"pattern": "read*", "action": "allow"}]
	    }},
	    {"name": "bad-default", "command": ["x"], "permissions": {"default": "maybe"}},
	    {"name": "empty-pattern", "command": ["x"], "permissions": {"rules": [{"pattern": "", "action": "allow"}]}},
	    {"name": "server-in-server", "command": ["x"], "permissions": {"rules": [{"server": "a", "pattern": "x", "action": "deny"}]}},
	    {"name": "plain", "command": ["x"]}
	  ]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Permissions == nil || cfg.Permissions.Default != PermissionAllow || len(cfg.Permissions.Rules) != 1 {
		t.Fatalf("global permissions not read: %+v", cfg.Permissions)
	}
	if got := cfg.Permissions.Rules[0]; got.Server != "a*" || got.Pattern != "write*" || got.Action != PermissionDeny || got.Message != "read only" {
		t.Errorf("global rule: %+v", got)
	}
	byName := map[string]*Server{}
	for _, s := range cfg.Servers {
		byName[s.Name] = s
	}
	a := byName["a"].Permissions
	if a == nil || a.Default != PermissionDeny || len(a.Rules) != 1 || a.Rules[0].Pattern != "read*" {
		t.Errorf("server permissions not read: %+v", a)
	}
	for _, name := range []string{"bad-default", "empty-pattern", "server-in-server"} {
		if byName[name].ConfigError == "" {
			t.Errorf("%s: want a config error for a broken permissions block", name)
		}
		if byName[name].Permissions != nil {
			t.Errorf("%s: broken block must be dropped", name)
		}
	}
	if byName["plain"].Permissions != nil || byName["plain"].ConfigError != "" {
		t.Error("a server without permissions must stay untouched")
	}
}

// A broken top-level block must be ignored: the set still serves everything.
func TestInvalidGlobalPermissionsIgnored(t *testing.T) {
	path := write(t, `{
	  "permissions": {"default": "maybe"},
	  "servers": [{"name": "a", "command": ["x"]}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a broken global permissions block must not fail the load: %v", err)
	}
	if cfg.Permissions != nil {
		t.Errorf("want the block dropped, got %+v", cfg.Permissions)
	}
	if len(cfg.Usable()) != 1 {
		t.Error("the server must stay usable")
	}
}

func TestIdentifiableFailsLoad(t *testing.T) {
	path := write(t, `{"servers": [{"nameless": true}]}`)
	if _, err := Load(path); err == nil {
		t.Fatal("a server without a name must fail the load")
	}
	path = write(t, `{"servers": [{"name": "ok", "command": ["x"], "command2": 1}`)
	if _, err := Load(path); err == nil {
		t.Fatal("broken JSON must fail the load")
	}
}

// enabled:false must be serialized: a partial update rebuilds the entry from
// the marshalled form, and a dropped field would silently flip the server on.
func TestEnabledFalseIsMarshalled(t *testing.T) {
	body, err := json.Marshal(&Server{Name: "off", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("enabled:false потеряно при сериализации: %s", body)
	}
}

// A string command is the executable name, not an argv line: arguments come
// only from `args`, like the TS reference.
func TestStringCommandStaysWhole(t *testing.T) {
	path := write(t, `{"servers":[
	  {"name":"str","command":"npx -y pkg"},
	  {"name":"str-args","command":"npx","args":["-y","pkg"]}
	]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	byName := map[string]*Server{}
	for _, s := range cfg.Servers {
		byName[s.Name] = s
	}
	if got := byName["str"].Command; len(got) != 1 || got[0] != "npx -y pkg" {
		t.Errorf("command строкой: want [\"npx -y pkg\"], got %v", got)
	}
	if got := byName["str-args"].Command; len(got) != 3 || got[2] != "pkg" {
		t.Errorf("command + args: want [npx -y pkg], got %v", got)
	}
}

// `${VAR}` and `{file:/abs}` are expanded at load, in env, headers and the
// transport token — otherwise a child process receives the literal reference.
func TestExpandSecrets(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_TEST_TOKEN", "from-env")
	path := write(t, fmt.Sprintf(`{
	  "servers": [{"name":"s","command":["x"],
	    "env":{"TOKEN":"${MCP_TEST_TOKEN}","FILE":"{file:%s}","LITERAL":"a$b"},
	    "headers":{"Authorization":"Bearer ${MCP_TEST_TOKEN}"}}],
	  "transport":{"type":"http","port":9100,"authToken":"${MCP_TEST_TOKEN}"}
	}`, secret))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Servers[0].ConfigError != "" {
		t.Fatalf("неожиданная ошибка: %s", cfg.Servers[0].ConfigError)
	}
	env := cfg.Servers[0].Env
	if env["TOKEN"] != "from-env" {
		t.Errorf("env.TOKEN: got %q", env["TOKEN"])
	}
	if env["FILE"] != "s3cret" {
		t.Errorf("env.FILE: хвостовой перевод строки не обрезан, got %q", env["FILE"])
	}
	if env["LITERAL"] != "a$b" {
		t.Errorf("строка без фигурных скобок изменена: %q", env["LITERAL"])
	}
	if got := cfg.Servers[0].Headers["Authorization"]; got != "Bearer from-env" {
		t.Errorf("headers.Authorization: got %q", got)
	}
	if got := cfg.Transport.AuthToken; got != "from-env" {
		t.Errorf("transport.authToken: got %q", got)
	}
}

// A broken reference is a soft ConfigError of that server, like any other
// validation defect.
func TestExpandSecretsSoftFail(t *testing.T) {
	path := write(t, `{"servers":[
	  {"name":"missing","command":["x"],"env":{"TOKEN":"${MCP_TEST_ABSENT_VAR}"}},
	  {"name":"relative","command":["x"],"env":{"TOKEN":"{file:rel/token}"}}
	]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("broken references must not fail the load: %v", err)
	}
	if got := cfg.Servers[0].ConfigError; !strings.Contains(got, "env.TOKEN") ||
		!strings.Contains(got, "MCP_TEST_ABSENT_VAR") {
		t.Errorf("незаданная переменная: got %q", got)
	}
	if got := cfg.Servers[1].ConfigError; !strings.Contains(got, "нужен абсолютный путь") {
		t.Errorf("относительный путь: got %q", got)
	}
	if len(cfg.Usable()) != 0 {
		t.Errorf("сломанные записи не должны быть пригодны: %d", len(cfg.Usable()))
	}
}

// Битый сервер не валит набор: он остаётся с ConfigError, остальные живы.
func TestBrokenServerIsSoftFail(t *testing.T) {
	path := write(t, `{"servers":[
	  {"name":"ok","command":["x"]},
	  {"name":"bad","command":["y"],"args":1}
	]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("битый сервер не должен валить загрузку: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("серверов: want 2, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].ConfigError != "" {
		t.Errorf("здоровый сервер помечен: %q", cfg.Servers[0].ConfigError)
	}
	if got := cfg.Servers[1].ConfigError; !strings.Contains(got, "сервер #2") {
		t.Errorf("битый сервер без пометки: %q", got)
	}
	if n := len(cfg.Usable()); n != 1 {
		t.Errorf("пригодных серверов: want 1, got %d", n)
	}
}

// Резервный ${VAR} в transport.authToken не раскрывается: токен ушёл бы
// заголовком нижестоящего сервера.
func TestReservedAuthTokenFailsLoad(t *testing.T) {
	path := write(t, `{
	  "transport": {"authToken": "${MCPRIG_PASSWORD}"},
	  "servers": [{"name": "a", "command": ["x"]}]
	}`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "transport.authToken: переменная ${MCPRIG_PASSWORD}") {
		t.Fatalf("want reserved authToken error, got %v", err)
	}
}

// HasFileRef ловит {file:…} в любом месте значения: префиксная проверка
// пропускала «p{file:/…}», и файл уходил вниз.
func TestHasFileRef(t *testing.T) {
	cases := map[string]bool{
		"{file:/etc/token}":    true,
		"Bearer {file:/x/y}":   true,
		"p{file:/home/env.js}": true,
		"plain":                false,
		"file:/etc/token":      false,
	}
	for in, want := range cases {
		if got := HasFileRef(in); got != want {
			t.Errorf("HasFileRef(%q) = %v, want %v", in, got, want)
		}
	}
}

// Резервные имена отвергаются на загрузке набора: в envAllow, в env и в
// ${VAR} заголовка — сервер иначе получает секрет менеджера.
func TestReservedEnvRejected(t *testing.T) {
	path := write(t, `{"servers":[
	  {"name":"allow","command":["x"],"envAllow":["MCPRIG_LEAKTEST"]},
	  {"name":"env","command":["x"],"env":{"OPENBAO_TOKEN":"x"}},
	  {"name":"header","command":["x"],"headers":{"Authorization":"Bearer ${MCPRIG_PROXY_SECRET}"}},
	  {"name":"ref","command":["x"],"env":{"T":"${BAO_TOKEN}"}}
	]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{
		"envAllow «MCPRIG_LEAKTEST»: резервный префикс",
		"env «OPENBAO_TOKEN»: резервный префикс",
		"headers.Authorization: переменная ${MCPRIG_PROXY_SECRET} — резервный префикс",
		"env.T: переменная ${BAO_TOKEN} — резервный префикс",
	}
	for i, w := range want {
		if got := cfg.Servers[i].ConfigError; !strings.Contains(got, w) {
			t.Errorf("сервер %d: want %q, got %q", i, w, got)
		}
	}
	if n := len(cfg.Usable()); n != 0 {
		t.Errorf("резервные серверы пригодны: %d", n)
	}
}
