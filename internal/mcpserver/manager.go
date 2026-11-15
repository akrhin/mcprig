// Package mcpserver aggregates a set's downstream servers into one MCP server
// exposing lazy-mcp's meta-tools. Sessions are opened on first use and
// shared by every later call.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/downstream"
	"github.com/akrhin/mcprig/internal/stats"
)

const (
	// healthTTL keeps one probe result (including a failure) before re-checking.
	// Должен совпадать с healthTTL в internal/manager/health.go: иначе панель и
	// прокси расходятся в том, кто «здоров».
	healthTTL = 15 * time.Second
	// healthTimeout bounds one server's status probe; такой же, как healthTimeout
	// в internal/manager/health.go. Холодный stdio (npx, uvx) и первый коннект к
	// LSP поднимаются дольше 15 с, и сервер выглядел мёртвым, хотя вызовы проходят.
	healthTimeout = 15 * time.Second
	// maxPages stops a server that keeps handing out cursors.
	maxPages = 20
	// maxProbes caps concurrent status probes in one list_servers call.
	maxProbes = 8
	// warmTimeout bounds one server's warm-up at start: холодный npx/uvx
	// распаковывается десятками секунд, но не бесконечно.
	warmTimeout = 2 * time.Minute
	// invokeRetryDelay is the pause before repeating an invoke whose connection
	// died before the first response byte.
	invokeRetryDelay = 250 * time.Millisecond
)

// Variables, not constants: a test of the hung-connect paths cannot spend 30
// seconds per case. Nothing writes them outside tests.
var (
	// connectTimeout bounds one connect+initialize: без дедлайна зависший
	// апстрим навсегда занимал бы c.try, и сервер считался бы живым.
	connectTimeout = 30 * time.Second
	// closeWait bounds how long Close waits for an in-flight connect.
	closeWait = 5 * time.Second
	// oauthConnectTimeout is the budget for a server whose first connect may
	// wait for a human: the SDK blocks the same dial while the browser hands
	// over the authorization code (up to 5 minutes).
	oauthConnectTimeout = 6 * time.Minute
)

var (
	errClosed    = errors.New("менеджер остановлен — перезапустите mcprig")
	errRetired   = errors.New("сервер изменён перезагрузкой конфига — вызов не выполнен, повторите")
	errNoTargets = errors.New("нужен параметр server или servers")
	// errProbeSkipped — проба вообще не началась (не дождалась слота): исход
	// неизвестен, это не отказ сервера.
	errProbeSkipped = errors.New("проверка не началась: не дождалась свободного слота")
)

// probeSlots bounds how many servers one list_servers probes at once.
var probeSlots = make(chan struct{}, maxProbes)

// dialer opens one downstream session. Tests point it at in-memory transports.
type dialer func(context.Context, *config.Server) (*mcp.ClientSession, error)

// Manager owns the downstream sessions of one set.
type Manager struct {
	// mu guards cfg, entries and limits: Reload swaps them whole and every
	// reader takes a snapshot. Ни один сетевой вызов под этим замком не идёт.
	mu sync.RWMutex
	// generation растёт на каждой попытке перечитать конфиг, включая
	// отвергнутую: прежний конфиг остаётся, но попытка видна супервизору.
	generation int

	cfg     *config.Config
	version string
	dial    dialer
	entries []*entry

	ctx    context.Context
	cancel context.CancelFunc

	// closed marks a stopped manager: a reload that lands after Close is
	// refused whole, so it neither swaps entries under Close nor adds to
	// closing, and closing.Wait cannot race Add.
	closed bool
	// closing tracks the goroutines that close dropped sessions after the ack.
	closing sync.WaitGroup

	// slots bounds simultaneous calls to one downstream inside invoke_many: a
	// per-request limiter let two parallel fan-outs send twice the limit.
	slots *serverSlots

	// limits is the set's call rate limiter; nil when the set has no rateLimit.
	limits *rateLimits

	// Stats, when set, counts every downstream invocation per client and
	// tool. The panel reads the recorder; the proxy works without it.
	Stats *stats.Recorder

	// LockPath names the set's approved-descriptor record. Empty disables the
	// quarantine: a manager without a data dir has nowhere to keep approvals.
	LockPath string
	// lockMu сериализует чтение-правку-запись записи карантина: два
	// одновременных list_commands иначе теряли бы запись друг друга.
	lockMu sync.Mutex

	// ToolOutURL, when set, names a truncated answer by its /tool-out URL
	// instead of a path: the caller reached this process over HTTP with a key
	// and fetches the file under the same guard as /mcp. stdio leaves it false.
	ToolOutURL bool
}

type entry struct {
	// srv меняется на перезагрузке конфига: указатель подменяется целиком,
	// поэтому читатель под замком не бывает и видит либо старый сервер, либо
	// новый, но не половину правки. Конфиг сервера неизменяем после Load.
	srv atomic.Pointer[config.Server]

	conn conn

	// secrets — значения env и заголовков сервера, которые нельзя выпускать
	// наружу: апстрим повторяет их в тексте ошибки.
	secrets []string

	healthMu sync.Mutex
	health   healthState

	// sandboxNote — причина, по которой запрошенная изоляция не применилась.
	sandboxMu   sync.Mutex
	sandboxNote string

	// tools кэширует tools/list до переподключения: без него каждый
	// list_commands заново обходил все апстримы (порт 205 мс у одного portainer).
	toolsMu sync.Mutex
	tools   []*mcp.Tool
}

func (e *entry) cachedTools() []*mcp.Tool {
	e.toolsMu.Lock()
	defer e.toolsMu.Unlock()
	return e.tools
}

// server is the entry's current server config. Atomic, not locked: the hot path
// calls it per request, and Reload swaps the pointer whole.
func (e *entry) server() *config.Server { return e.srv.Load() }

// newEntry builds an entry with its secrets taken from the same config.
func newEntry(srv *config.Server) *entry {
	e := &entry{secrets: serverSecrets(srv)}
	e.srv.Store(srv)
	return e
}

// findTool returns the named tool, or nil when the list does not have it.
func findTool(tools []*mcp.Tool, name string) *mcp.Tool {
	for _, t := range tools {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func (m *Manager) storeTools(e *entry, tools []*mcp.Tool) {
	e.toolsMu.Lock()
	e.tools = tools
	e.toolsMu.Unlock()
	m.observe(e, tools)
	m.publishSchema()
}

// dropSession closes the cached session and the tool list that came with it.
func (m *Manager) dropSession(e *entry) {
	e.conn.reset()
	m.storeTools(e, nil)
}

// retireSession closes an entry a reload dropped. markRetired comes first: a
// call that already holds the stale entry must not dial it again with the old
// config, and retryableCall must not treat the refusal as a dropped connection.
// A call already past session() keeps running on the old process.
func (m *Manager) retireSession(e *entry) {
	e.conn.markRetired()
	// Ошибку некуда доложить: запись отброшена, а сессию повторно закрывает
	// Manager.Close.
	_ = e.conn.close()
	m.storeTools(e, nil)
}

// publishSchema pushes the whole-set list_commands weight to the panel's
// recorder. Bytes are the JSON of the same answer list_commands builds from
// the cache, so the number costs no downstream call. A server that has no
// cached tools yet is simply absent, as it would be from a live answer.
func (m *Manager) publishSchema() {
	if m.Stats == nil {
		return
	}
	out := make(map[string][]commandInfo)
	tools := 0
	for _, e := range m.allEntries() {
		srv := e.server()
		cached := e.cachedTools()
		if len(cached) == 0 {
			continue
		}
		cmds := make([]commandInfo, 0, len(cached))
		for _, tool := range cached {
			cmds = append(cmds, commandInfo{Server: srv.Name, Name: tool.Name, Command: tool.Name, Description: tool.Description})
		}
		out[srv.Name] = cmds
		tools += len(cmds)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return
	}
	size := int64(len(raw))
	m.Stats.SetSchema(stats.Schema{Bytes: size, Tokens: stats.TokensOf(size), Tools: tools})
}

// healthState is one cached probe result.
type healthState struct {
	at      time.Time
	ok      bool
	errText string
}

// New builds a manager. Nothing is connected until a tool needs a server.
// A connector carries the version and, when present, OAuth settings.
func New(cfg *config.Config, version string, conn *downstream.Connector) *Manager {
	if conn == nil {
		// Env: без whitelist ребёнок получил бы всё окружение вызывающего,
		// включая секреты хоста.
		conn = &downstream.Connector{Version: version, Env: downstream.EnvFor}
	}
	if conn.Version == "" {
		conn.Version = version
	}
	m := &Manager{
		cfg:     cfg,
		version: version,
		slots:   newServerSlots(maxPerServer),
		limits:  newRateLimits(cfg.RateLimit),
		dial: func(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
			return conn.Connect(ctx, srv)
		},
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	for _, srv := range cfg.Servers {
		m.entries = append(m.entries, newEntry(srv))
	}
	// A downstream that gains or drops tools at runtime says so with
	// notifications/tools/list_changed. Dropping the cached list here makes the
	// next list_commands see the change without refresh: true. A caller-supplied
	// hook wins.
	if conn.OnSandboxApplied == nil {
		conn.OnSandboxApplied = func(srv *config.Server) {
			if e := m.find(srv.Name); e != nil {
				e.sandboxMu.Lock()
				e.sandboxNote = ""
				e.sandboxMu.Unlock()
			}
		}
	}
	if conn.OnSandboxFallback == nil {
		conn.OnSandboxFallback = func(srv *config.Server, reason string) {
			if e := m.find(srv.Name); e != nil {
				e.sandboxMu.Lock()
				e.sandboxNote = reason
				e.sandboxMu.Unlock()
			}
		}
	}
	if conn.OnToolsChanged == nil {
		conn.OnToolsChanged = func(srv *config.Server) {
			if e := m.find(srv.Name); e != nil {
				m.storeTools(e, nil)
			}
		}
	}
	return m
}

// Close cancels in-flight connects and shuts every open downstream session.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	entries := m.allEntries()
	// Mark the conns closed before cancelling the context: a caller released by
	// the cancel must hear "менеджер закрыт", not "context canceled".
	for _, e := range entries {
		e.conn.markClosed()
	}
	m.cancel()
	// Сессии, отброшенные перезагрузкой, закрывает своя горутина: без ожидания
	// тест кончается раньше, чем закрылась сессия, и горутина утекает.
	m.closing.Wait()
	var errs []error
	for _, e := range entries {
		if err := e.conn.close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReloadResult reports what one attempt to re-read the config applied, and what
// it could not. It is the whole answer the supervisor gets: the set's process
// writes it to reload.status, there is no HTTP route for it.
type ReloadResult struct {
	// Generation counts attempts, starting at 1, including a rejected config:
	// the supervisor sees that its SIGHUP landed even when nothing was applied.
	Generation int
	// Applied names what the live process now uses: "added:<server>" (lazy — no
	// connection until a call), "removed:<server>", "reset:<server>" (session
	// closed: a field that decides the process or address changed),
	// "updated:<server>" (applied in place, session kept), and the set-wide
	// "rateLimit", "permissions", "profiles", "requestTimeout", "healthMonitor".
	Applied []string
	// RestartNeeded names the transport fields this process cannot adopt: the
	// listener was built at startup, so only a restart applies them.
	RestartNeeded []string
	// Err is the load error. Applied stays empty and the previous config is
	// still in force: fail-closed, the process keeps serving.
	Err string
}

// Reload applies a freshly loaded config to the running process: new servers
// join lazily, removed ones lose their session and tool cache, a server whose
// process or address changed gets a new session on its next call. The previous
// config stays in force only when the caller could not load one at all.
func (m *Manager) Reload(next *config.Config) ReloadResult {
	_, span := otel.Tracer(tracerName).Start(m.ctx, reloadSpan)
	defer span.End()
	res := ReloadResult{Applied: []string{}, RestartNeeded: []string{}}
	var drop []*entry

	m.mu.Lock()
	if m.closed {
		// Перезагрузка после Close ничего не применяет: иначе она подменила бы
		// entries у уже снятого снимка, и отброшенные записи не закрылись бы.
		generation := m.generation
		m.mu.Unlock()
		res.Generation = generation
		res.Err = errClosed.Error()
		return res
	}
	m.generation++
	res.Generation = m.generation
	old := m.cfg
	// Лимит меняется только правкой rateLimit: иначе перезагрузка обнуляла бы
	// счётчики, и клиент обходил бы лимит, дёргая reload.
	limits := m.limits
	if !reflect.DeepEqual(old.RateLimit, next.RateLimit) {
		limits = newRateLimits(next.RateLimit)
	}
	fresh := make([]*entry, 0, len(next.Servers))
	// Removals first, in the old order: a deterministic status file matters more
	// than saving a map.
	for _, e := range m.entries {
		if name := e.server().Name; !hasServer(next.Servers, name) {
			drop = append(drop, e)
			res.Applied = append(res.Applied, "removed:"+name)
		}
	}
	for _, srv := range next.Servers {
		name := srv.Name
		e := findEntry(m.entries, name)
		switch {
		case e == nil:
			fresh = append(fresh, newEntry(srv))
			res.Applied = append(res.Applied, "added:"+name)
		case launchChanged(e.server(), srv):
			// Правка сервера закрывает сессию для новых вызовов; вызовы в
			// полёте не дренируются — их обрыв ловится обычным правилом
			// повтора (retryableCall) и уходит на новую сессию.
			drop = append(drop, e)
			fresh = append(fresh, newEntry(srv))
			res.Applied = append(res.Applied, "reset:"+name)
		case !reflect.DeepEqual(e.server(), srv):
			e.srv.Store(srv)
			fresh = append(fresh, e)
			res.Applied = append(res.Applied, "updated:"+name)
		default:
			fresh = append(fresh, e)
		}
	}
	m.entries = fresh
	m.limits = limits
	res.RestartNeeded = transportRestart(old, next)
	changes := setChanges(old, next)
	if len(res.RestartNeeded) > 0 {
		// Отвергнутые транспортом поля остаются прежними: иначе менеджер
		// докладывал бы конфиг, которого на проводе нет. Профили откатываются
		// вместе с ним, поэтому и в Applied их нет.
		next = keepLiveTransport(old, next)
		changes = slices.DeleteFunc(changes, func(name string) bool { return name == "profiles" })
	}
	res.Applied = append(res.Applied, changes...)
	m.cfg = next
	if len(drop) > 0 {
		m.closing.Add(1)
	}
	m.mu.Unlock()

	// Сессии отброшенных записей закрываются после ответа: супервизор ждёт
	// reload.status 3 с, а зависшая попытка подключения держит close до
	// closeWait (5 с). shortcut: окно между ack и закрытием — до closeWait;
	// Manager.Close ждёт эти горутины.
	if len(drop) > 0 {
		go func() {
			defer m.closing.Done()
			for _, e := range drop {
				m.retireSession(e)
			}
		}()
	}
	m.publishSchema()
	if span.IsRecording() {
		span.SetAttributes(
			attribute.Int("mcprig.reload.generation", res.Generation),
			attribute.StringSlice("mcprig.reload.applied", res.Applied),
		)
	}
	return res
}

// ReloadFailed records an attempt whose file did not even load. The running
// config is untouched; the generation still grows so the supervisor's answer
// names the try that failed.
func (m *Manager) ReloadFailed(err error) ReloadResult {
	m.mu.Lock()
	m.generation++
	generation := m.generation
	m.mu.Unlock()
	return ReloadResult{Generation: generation, Applied: []string{}, RestartNeeded: []string{}, Err: err.Error()}
}

func hasServer(servers []*config.Server, name string) bool {
	for _, srv := range servers {
		if srv.Name == name {
			return true
		}
	}
	return false
}

func findEntry(entries []*entry, name string) *entry {
	for _, e := range entries {
		if e.server().Name == name {
			return e
		}
	}
	return nil
}

// launchChanged reports whether a change decides the downstream process or the
// address it is reached at: such a server needs a fresh session, and its tool
// list may differ. Permissions, enabled, tags and description do not.
func launchChanged(a, b *config.Server) bool {
	return a.Transport != b.Transport || a.URL != b.URL || a.Cwd != b.Cwd ||
		!slices.Equal(a.Command, b.Command) ||
		!maps.Equal(a.Env, b.Env) || !slices.Equal(a.EnvAllow, b.EnvAllow) ||
		!maps.Equal(a.Headers, b.Headers) ||
		!reflect.DeepEqual(a.OAuth, b.OAuth) || !reflect.DeepEqual(a.Sandbox, b.Sandbox)
}

// transportRestart names the listener fields that a running process cannot
// adopt: bind address, port, path, protocol and the Host list are fixed at
// start. Tokens, openness, clientNames and keyProfiles are absent on purpose:
// reloadOnce hands all of them to the live listener (SetAuth), and the manager
// resolves the profile per request — so a key rotation needs no restart.
func transportRestart(old, next *config.Config) []string {
	out := []string{}
	o, n := old.Transport, next.Transport
	switch {
	case o == nil && n == nil:
		return out
	case o == nil || n == nil:
		return append(out, "transport")
	}
	if o.Host != n.Host {
		out = append(out, "host")
	}
	if o.Port != n.Port {
		out = append(out, "port")
	}
	if o.Path != n.Path {
		out = append(out, "path")
	}
	if o.Type != n.Type {
		out = append(out, "type")
	}
	if !slices.Equal(o.AllowedHosts, n.AllowedHosts) {
		out = append(out, "allowedHosts")
	}
	return out
}

// keepLiveTransport puts the listener's live transport back wholesale, so the
// manager's config names what is really in force. keyProfiles goes with it: the
// profile table is derived from the same keys as the token list, and a table
// from one generation under another is a mix — a key could get its peer's
// rules. A transport block that disappears from the file keeps the live one for
// the same reason.
func keepLiveTransport(old, next *config.Config) *config.Config {
	out := *next
	out.Transport = old.Transport
	// Таблица профилей идёт вместе с транспортом: отображение ключ→профиль
	// осталось прежним, и новая таблица под ним была бы смесью поколений —
	// ключ прежнего токена получил бы чужие правила.
	out.Profiles = old.Profiles
	return &out
}

// setChanges names the set-wide blocks a reload applied in place.
func setChanges(old, next *config.Config) []string {
	var out []string
	if !reflect.DeepEqual(old.RateLimit, next.RateLimit) {
		out = append(out, "rateLimit")
	}
	if !reflect.DeepEqual(old.Permissions, next.Permissions) {
		out = append(out, "permissions")
	}
	if !reflect.DeepEqual(old.Profiles, next.Profiles) {
		out = append(out, "profiles")
	}
	if old.RequestTimeout != next.RequestTimeout {
		out = append(out, "requestTimeout")
	}
	if !reflect.DeepEqual(old.HealthMonitor, next.HealthMonitor) {
		out = append(out, "healthMonitor")
	}
	return out
}

func (m *Manager) Server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "mcprig", Version: m.version}, nil)
	s.AddReceivingMiddleware(traceCalls)
	s.AddTool(&mcp.Tool{
		Name: "list_servers",
		Description: "List all available MCP servers with their capabilities and tags. " +
			"Use the filter parameter to search by name, description, or tag. " +
			"Includes health status for each server.",
		InputSchema: objectSchema(map[string]any{
			"filter": map[string]any{"type": "string", "description": "Optional: filter servers by name, description, or tag keyword"},
		}),
	}, m.recordMeta("list_servers", m.listServers))
	s.AddTool(&mcp.Tool{
		Name: "list_commands",
		Description: "List commands from one server (server) or several at once (servers). " +
			"One of server or servers is required. Use prefix to keep only tool names starting with it. " +
			"An answer over maxBytes is cut and the full one is written to a file.",
		InputSchema: objectSchema(map[string]any{
			"server": map[string]any{"type": "string", "description": "Server name; returns an array of commands"},
			"servers": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Server names; returns an object keyed by server name",
			},
			"prefix": map[string]any{
				"type":        "string",
				"description": "Optional: keep only commands whose name starts with this prefix (case-insensitive)",
			},
			"namesOnly": map[string]any{
				"type":        "boolean",
				"description": "Optional: return only tool names: an array of strings for server, arrays keyed by name for servers. Cheaper than full catalog; use describe_commands for one schema",
			},
			"refresh": map[string]any{
				"type":        "boolean",
				"description": "Optional: drop the cached catalog and re-read tools/list from every server",
			},
			"maxBytes": map[string]any{
				"type":        "integer",
				"description": "Optional: cap on the answer in bytes (max 1048576); without it the answer is not cut. A cut answer names the file with the full one",
			},
		}),
	}, m.recordMeta("list_commands", m.listCommands))
	s.AddTool(&mcp.Tool{
		Name: "describe_commands",
		Description: "Get detailed schemas for specific commands from a server. " +
			"An answer over maxBytes is cut and the full one is written to a file.",
		InputSchema: objectSchema(map[string]any{
			"server": map[string]any{"type": "string", "description": "Server name"},
			"command_names": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of command names to describe",
			},
			"refresh": map[string]any{
				"type":        "boolean",
				"description": "Optional: drop the cached catalog and re-read tools/list first",
			},
			"maxBytes": map[string]any{
				"type":        "integer",
				"description": "Optional: cap on the answer in bytes (max 1048576); without it the answer is not cut. A cut answer names the file with the full one",
			},
		}, "server", "command_names"),
	}, m.recordMeta("describe_commands", m.describeCommands))
	s.AddTool(&mcp.Tool{
		Name: "invoke_command",
		Description: "Execute a command from a specific server (auto-discovers if needed). " +
			"This is a wrapper tool: keep only server, command_name (or command) and maxBytes at the top level, " +
			"and put downstream tool inputs inside parameters.",
		InputSchema: withAnyOf(objectSchema(map[string]any{
			"server":       map[string]any{"type": "string", "description": "Server name"},
			"command_name": map[string]any{"type": "string", "description": "Command name to execute"},
			"command":      map[string]any{"type": "string", "description": "Alias for command_name; list_commands returns the name in the command field"},
			"parameters": map[string]any{
				"type":        "object",
				"description": "Optional downstream command input object. Put the target command inputs here, not alongside server or command_name.",
			},
			"maxBytes": map[string]any{
				"type":        "integer",
				"description": "Optional: cap the returned answer at this many bytes (default 32768, at most 1048576). A longer answer is cut and the full one is saved to a file named in the marker.",
			},
		}, "server"), "command_name", "command"),
	}, m.invokeCommand)
	s.AddTool(&mcp.Tool{
		Name: "invoke_many",
		Description: "Выполнить до 8 независимых вызовов одним запросом. " +
			"Каждый вызов — как invoke_command: server, command_name (синоним — command) и parameters. " +
			"Ответ — массив в порядке calls; ошибка одного вызова не отменяет остальные.",
		InputSchema: objectSchema(map[string]any{
			"calls": map[string]any{
				"type":        "array",
				"description": "Вызовы (не больше 8): server, command_name (или command) и необязательные parameters",
				"items": withAnyOf(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"server":       map[string]any{"type": "string", "description": "Server name"},
						"command_name": map[string]any{"type": "string", "description": "Command name to execute"},
						"command":      map[string]any{"type": "string", "description": "Alias for command_name (synonym)"},
						"parameters": map[string]any{
							"type":        "object",
							"description": "Optional downstream command input object. Put the target command inputs here.",
						},
					},
					"required": []string{"server"},
				}, "command_name", "command"),
			},
			"maxBytes": map[string]any{
				"type":        "integer",
				"description": "Optional: cap each returned answer at this many bytes (default 32768, at most 1048576).",
			},
		}, "calls"),
	}, m.invokeMany)
	m.addResourceTools(s)
	return s
}

// recordMeta учитывает и мета-вызов в счётчиках и истории: панель «Вызовы»
// показывает их на псевдосервере "mcprig", а не только дочерние инструменты.
// Метка клиента та же, что у invoke_command: labelOf берёт её из _meta и
// initialize, а не из уровня обработчика.
func (m *Manager) recordMeta(name string, fn mcp.ToolHandler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		res, err := fn(ctx, req)
		recErr := err
		if recErr == nil && res != nil && res.IsError {
			recErr = errors.New(resultMessage(res))
		}
		if m.Stats != nil {
			m.Stats.Record(labelOf(ctx, req), "mcprig", name, time.Since(start), recErr)
		}
		return res, err
	}
}

// ─── list_servers ─────────────────────────────────────────────────────────────

type listServersInput struct {
	Filter string `json:"filter,omitempty"`
}

type serverInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	Enabled     bool   `json:"enabled"`
	Healthy     bool   `json:"healthy"`
	// Unknown — проба не началась (не дождалась слота): исход неизвестен,
	// и healthy:true здесь значит «не больной», а не «проверен».
	Unknown bool     `json:"unknown,omitempty"`
	Error   string   `json:"error,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	// Isolation — "bwrap", когда песочница применена, и "без изоляции: …",
	// когда она запрошена, но недоступна. Пусто — сервер без песочницы.
	Isolation string `json:"isolation,omitempty"`
}

func (m *Manager) listServers(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in listServersInput
	if err := strictArgs(req, &in, "", "filter"); err != nil {
		return errResult(err)
	}
	// Filtering happens before any probe: a narrow filter must not dial the set.
	targets := m.allEntries()
	if filter := strings.ToLower(in.Filter); filter != "" {
		kept := make([]*entry, 0, len(targets))
		for _, e := range targets {
			if matches(e.server(), filter) {
				kept = append(kept, e)
			}
		}
		targets = kept
	}
	out := struct {
		Servers []serverInfo `json:"servers"`
		Version string       `json:"version"`
		PID     int          `json:"pid"`
	}{Servers: make([]serverInfo, len(targets)), Version: m.version, PID: os.Getpid()}

	var wg sync.WaitGroup
	for i, e := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv := e.server()
			healthy, errText, unknown := m.status(ctx, e)
			out.Servers[i] = serverInfo{
				Name:        srv.Name,
				Description: srv.Description,
				Type:        transportType(srv),
				Enabled:     srv.Enabled,
				Healthy:     healthy,
				Unknown:     unknown,
				Error:       errText,
				Tags:        srv.Tags,
				Isolation:   e.isolationNote(),
			}
		}()
	}
	wg.Wait()
	return jsonResult(out)
}

// matches reports whether name, description or a tag carries the filter.
func matches(srv *config.Server, lowerFilter string) bool {
	haystack := srv.Name + " " + srv.Description + " " + strings.Join(srv.Tags, " ")
	return strings.Contains(strings.ToLower(haystack), lowerFilter)
}

// status reports health from the TTL cache or one bounded probe. A server
// switched off on purpose is not unhealthy: nothing is dialed for it. Третий
// результат — unknown: проба не началась, исхода нет ни в кэше, ни из сокета.
func (m *Manager) status(ctx context.Context, e *entry) (bool, string, bool) {
	srv := e.server()
	if srv.ConfigError != "" {
		return false, srv.ConfigError, false
	}
	if !srv.Enabled {
		return true, "", false
	}
	e.healthMu.Lock()
	h := e.health
	e.healthMu.Unlock()
	if !h.at.IsZero() && time.Since(h.at) < healthTTL {
		return h.ok, h.errText, false
	}

	var ok bool
	var errText string
	err := m.probe(ctx, e)
	if errors.Is(err, errProbeSkipped) {
		// Исход неизвестен (проба не началась) — не кэшируем и не считаем больным.
		return true, "", true
	}
	if err != nil {
		errText = err.Error()
	} else {
		ok = true
	}
	e.healthMu.Lock()
	e.health = healthState{at: time.Now(), ok: ok, errText: errText}
	e.healthMu.Unlock()
	return ok, errText, false
}

// probe connects within healthTimeout so one hung server cannot stall a list.
func (m *Manager) probe(ctx context.Context, e *entry) error {
	probeCtx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	select {
	case probeSlots <- struct{}{}:
		defer func() { <-probeSlots }()
	case <-probeCtx.Done():
		return fmt.Errorf("%w: %w", errProbeSkipped, probeCtx.Err())
	}
	_, err := e.conn.session(probeCtx, m.dialFor(e.server()), connectDeadline(e.server()))
	return err
}

// ─── list_commands ────────────────────────────────────────────────────────────

type listCommandsInput struct {
	Server    string   `json:"server,omitempty"`
	Servers   []string `json:"servers,omitempty"`
	Prefix    string   `json:"prefix,omitempty"`
	Refresh   bool     `json:"refresh,omitempty"`
	NamesOnly bool     `json:"namesOnly,omitempty"`
	MaxBytes  int      `json:"maxBytes,omitempty"`
}

type commandInfo struct {
	Server      string `json:"server"`
	Name        string `json:"name"`
	Command     string `json:"command"` // kept for older callers; same as name
	Description string `json:"description,omitempty"`
	// Changed marks a tool whose descriptor no longer matches the approved
	// hash: it is listed and described, but invoke refuses it until approved.
	Changed bool `json:"changed,omitempty"`
}

// refreshNames — серверы, чей кэш обновляется по refresh. Без имён чистить
// нечего: запрос без server/servers ниже всё равно кончается errNoTargets.
func (in listCommandsInput) refreshNames() []string {
	names := make([]string, 0, len(in.Servers)+1)
	if in.Server != "" {
		names = append(names, in.Server)
	}
	return append(names, in.Servers...)
}

func (m *Manager) listCommands(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in listCommandsInput
	if err := strictArgs(req, &in, "", "server", "servers", "prefix", "refresh", "namesOnly", "maxBytes"); err != nil {
		return errResult(err)
	}
	// No maxBytes in the request — no cap: a catalog used to arrive whole, and
	// a 32 KiB cut of JSON is not JSON at all.
	maxBytes := 0
	if in.MaxBytes > 0 {
		var err error
		if maxBytes, err = resolveMaxBytes(in.MaxBytes); err != nil {
			return errResult(err)
		}
	}
	if in.Refresh {
		// Только запрошенные серверы: refresh одного не должен опустошать кэш
		// остальных — по нему сверяется карантин, и пустой кэш открывал обход.
		for _, name := range in.refreshNames() {
			if e := m.find(name); e != nil {
				m.storeTools(e, nil)
			}
		}
	}
	// Профиль читается один раз: у ключа он один на все серверы запроса.
	profileName, profile := callerProfile(ctx, m.config())
	switch {
	case in.Server != "" && len(in.Servers) > 0:
		return errResult(errors.New("укажите server или servers, но не оба"))
	case in.Server != "":
		cmds, err := m.commandsOf(ctx, in.Server)
		if err != nil {
			return errResult(err)
		}
		return m.jsonTruncResult(filterCommands(profileCommands(profileName, profile, cmds), in.Prefix, in.NamesOnly), maxBytes)
	case len(in.Servers) > 0:
		// Ответ остальных не теряется из-за одного упавшего: у него в карте
		// {"error": "…"}, ошибку отдаём, только если не ответил никто.
		out := make(map[string]any, len(in.Servers))
		var firstErr error
		var mu sync.Mutex
		failed := 0
		// Один общий бюджет на весь обзор: иначе десять зависших серверов дают
		// десять requestTimeout подряд, и клиент ждёт часы.
		ctx, cancel := m.callCtx(ctx)
		defer cancel()
		// Параллельно: последовательный обход стоил 300 мс на одиннадцати серверах,
		// хотя каждый отвечает за 8–10 мс. Порядок ответа задаёт in.Servers.
		const parallel = 8
		sem := make(chan struct{}, parallel)
		var wg sync.WaitGroup
		for _, name := range in.Servers {
			wg.Add(1)
			sem <- struct{}{}
			go func(name string) {
				defer wg.Done()
				defer func() { <-sem }()
				cmds, err := m.commandsOf(ctx, name)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					out[name] = map[string]string{"error": err.Error()}
					failed++
					if firstErr == nil {
						firstErr = fmt.Errorf("%s: %w", name, err)
					}
					return
				}
				out[name] = filterCommands(profileCommands(profileName, profile, cmds), in.Prefix, in.NamesOnly)
			}(name)
		}
		wg.Wait()
		if failed == len(in.Servers) {
			return errResult(firstErr)
		}
		return m.jsonTruncResult(out, maxBytes)
	}
	return errResult(errNoTargets)
}

func (m *Manager) commandsOf(ctx context.Context, name string) ([]commandInfo, error) {
	ctx, cancel := m.callCtx(ctx)
	defer cancel()
	e := m.find(name)
	if e == nil {
		return nil, fmt.Errorf("неизвестный сервер %q", name)
	}
	srv := e.server()
	if !usable(srv) {
		return nil, serverProblem(srv)
	}
	if tools := e.cachedTools(); tools != nil {
		return m.commandList(name, tools), nil
	}
	var tools []*mcp.Tool
	err := m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		var err error
		tools, err = m.toolsOf(ctx, e, sess)
		return err
	})
	if err != nil {
		return nil, err
	}
	return m.commandList(name, tools), nil
}

// ─── describe_commands ────────────────────────────────────────────────────────

type describeCommandsInput struct {
	Server       string   `json:"server"`
	CommandNames []string `json:"command_names"`
	Refresh      bool     `json:"refresh,omitempty"`
	MaxBytes     int      `json:"maxBytes,omitempty"`
}

type commandSchema struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	InputSchema  any    `json:"inputSchema,omitempty"`
	OutputSchema any    `json:"outputSchema,omitempty"`
}

type describeOut struct {
	Commands []commandSchema `json:"commands"`
	Missing  []string        `json:"missing"`
}

func (m *Manager) describeCommands(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := m.callCtx(ctx)
	defer cancel()
	var in describeCommandsInput
	if err := strictArgs(req, &in, "", "server", "command_names", "refresh", "maxBytes"); err != nil {
		return errResult(err)
	}
	// Same as list_commands: only an explicit cap cuts the answer.
	maxBytes := 0
	if in.MaxBytes > 0 {
		var err error
		if maxBytes, err = resolveMaxBytes(in.MaxBytes); err != nil {
			return errResult(err)
		}
	}
	if in.Server == "" {
		return errResult(errors.New("server обязателен"))
	}
	e := m.find(in.Server)
	if e == nil {
		return errResult(fmt.Errorf("неизвестный сервер %q", in.Server))
	}
	if srv := e.server(); !usable(srv) {
		return errResult(serverProblem(srv))
	}
	if in.Refresh {
		m.storeTools(e, nil)
	}
	var tools []*mcp.Tool
	if cached := e.cachedTools(); cached != nil {
		tools = cached
	} else if err := m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		var err error
		tools, err = m.toolsOf(ctx, e, sess)
		return err
	}); err != nil {
		return errResult(err)
	}
	byName := make(map[string]*mcp.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	out := describeOut{Commands: make([]commandSchema, 0, len(in.CommandNames)), Missing: []string{}}
	for _, name := range in.CommandNames {
		tool := byName[name]
		if tool == nil {
			// One absent name must not hide the schemas that do exist.
			out.Missing = append(out.Missing, name)
			continue
		}
		out.Commands = append(out.Commands, commandSchema{
			Name:         tool.Name,
			Description:  tool.Description,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
		})
	}
	return m.jsonTruncResult(out, maxBytes)
}

// ─── invoke_command ───────────────────────────────────────────────────────────

type invokeCommandInput struct {
	Server      string          `json:"server"`
	CommandName string          `json:"command_name"`
	Command     string          `json:"command"` // синоним command_name: list_commands называет это поле command
	Parameters  json.RawMessage `json:"parameters"`
	MaxBytes    int             `json:"maxBytes,omitempty"`
}

func (m *Manager) invokeCommand(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in invokeCommandInput
	if err := strictArgs(req, &in, "аргументы инструмента кладутся в parameters", "server", "command_name", "command", "parameters", "maxBytes"); err != nil {
		return errResult(err)
	}
	if in.CommandName == "" {
		in.CommandName = in.Command
	} else if in.Command != "" && in.Command != in.CommandName {
		return errResult(errors.New("укажите command_name (или command)"))
	}
	if in.Server == "" {
		return errResult(errors.New("server обязателен"))
	}
	if in.CommandName == "" {
		return errResult(errors.New("укажите command_name (или command)"))
	}
	maxBytes, err := resolveMaxBytes(in.MaxBytes)
	if err != nil {
		return errResult(err)
	}
	e := m.find(in.Server)
	if e == nil {
		return errResult(fmt.Errorf("неизвестный сервер %q", in.Server))
	}
	// Снимок конфига сервера на весь вызов: если перезагрузка подменит его
	// посередине, вызов дорабатывает по снимку, а не смешивает два конфига.
	srv := e.server()
	// В спане — только имена: ни ключ набора, ни отпечаток ключа.
	if span := spanOf(ctx); span != nil {
		span.SetAttributes(
			attribute.String("mcp.server.name", srv.Name),
			attribute.String("mcp.command.name", in.CommandName),
		)
	}
	// Лимит — до открытия сессии набора: отказ не должен поднимать апстрим.
	if err := m.throttle(ctx, req, srv.Name, in.CommandName); err != nil {
		return errResult(err)
	}
	if !usable(srv) {
		return errResult(serverProblem(srv))
	}
	if message, denied := m.gate(ctx, srv.Name, in.CommandName, srv.Permissions); denied {
		return blockedResult(srv.Name, in.CommandName, message), nil
	}
	// Каталог нужен до вызова: по нему сверяется карантин и отбивается
	// неизвестное имя. Пустой кэш его не отменяет — см. toolForCall.
	tool, err := m.toolForCall(ctx, e, srv, in.CommandName)
	if err != nil {
		return errResult(err)
	}
	// Карантин — после политики и профиля, но до вызова вниз: изменившийся
	// дескриптор не должен дойти до апстрима без одобрения человека.
	if tool != nil {
		if message, changed := m.quarantine(srv.Name, tool); changed {
			return quarantineResult(message), nil
		}
	}

	args, err := parametersOf(in.Parameters)
	if err != nil {
		return errResult(err)
	}

	callCtx, cancel := m.callCtx(ctx)
	defer cancel()
	res, err := m.callWithRetry(callCtx, labelOf(ctx, req), e, in.CommandName, args)
	if err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			return errResult(fmt.Errorf("таймаут вызова %d мс", m.config().RequestTimeout))
		}
		return errResult(err)
	}
	if res == nil {
		return res, nil
	}
	return m.truncateResult(res, maxBytes)
}

// callWithRetry runs one downstream call under invoke_command's retry rule and
// counts the outcome. Only a drop before the first response byte is repeated,
// so a side effect is never performed twice.
func (m *Manager) callWithRetry(ctx context.Context, label string, e *entry, name string, args any) (*mcp.CallToolResult, error) {
	// Трасса родителя уходит вниз только как traceparent: прочие ключи _meta
	// принадлежат клиенту, а не апстриму.
	params := &mcp.CallToolParams{Name: name, Arguments: args, Meta: traceMeta(ctx)}
	start := time.Now()
	res, err := m.callTool(ctx, e, params)
	if retryableCall(res, err) {
		// Only a drop before any response byte reaches here: nothing was
		// returned, so the call did not produce a result to hand back. Repeat it
		// once on a fresh session, after a short pause, and let the second
		// failure speak for itself.
		m.dropSession(e)
		select {
		case <-time.After(invokeRetryDelay):
			res, err = m.callTool(ctx, e, params)
		case <-ctx.Done():
		}
	}
	// Счётчик пишется только по разрешённому имени: апстрим ответил (res != nil),
	// либо ошибки не было вовсе. Неизвестный инструмент и несостоявшееся
	// соединение дают res == nil — такие имена не должны заводить счётчики,
	// иначе память растёт от любого выдуманного command_name.
	// Инструмент может вернуть ошибку содержимым (isError) вместо ошибки
	// протокола — для счётчика это провал, иначе «ошибки» в панели всегда 0.
	// Секреты маскируются до счётчика: ошибка апстрима повторяет ключ в теле,
	// а он не должен попасть ни в контекст модели, ни в выгрузку tool-out.
	res = scrubSecrets(res, e.secrets)
	recErr := err
	if recErr == nil && res != nil && res.IsError {
		recErr = errors.New(resultMessage(res))
	}
	if m.Stats != nil && (err == nil || res != nil) {
		m.Stats.Record(label, e.server().Name, name, time.Since(start), recErr)
	}
	return res, err
}

// resolveMaxBytes validates the optional cap and applies the default.
func resolveMaxBytes(requested int) (int, error) {
	if requested < 0 {
		return 0, errors.New("maxBytes: не может быть отрицательным")
	}
	if requested > maxAllowedBytes {
		return 0, fmt.Errorf("maxBytes: допустимо не больше %d байт", maxAllowedBytes)
	}
	if requested == 0 {
		return defaultMaxBytes, nil
	}
	return requested, nil
}

// parametersOf decodes the wrapper's parameters: absent and null mean {}.
func parametersOf(raw json.RawMessage) (any, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return map[string]any{}, nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, errors.New("parameters: ожидался объект")
	}
	return raw, nil
}

// ─── shared helpers ───────────────────────────────────────────────────────────

// labelOf names the harness behind a call. The auth key fingerprint is never
// dropped: a stateless HTTP caller sends _meta clientInfo on every request and
// has no initialize handshake, so without this the key prefix would vanish.
// The label is "key:<fp>/<clientInfo>", or the transport label when the caller
// names itself nowhere, and only a caller with neither is "unknown".
func labelOf(ctx context.Context, req *mcp.CallToolRequest) string {
	label := stats.ClientFrom(ctx, "")
	who := clientInfoOf(req)
	if fp, ok := strings.CutPrefix(label, "key:"); ok {
		if who == "" {
			return label
		}
		// transport label carries "key:<fp>" or "key:<fp>/<user-agent>".
		fp, _, _ = strings.Cut(fp, "/")
		return "key:" + fp + "/" + who
	}
	if who != "" {
		return who
	}
	// ClientFrom уже подставляет "unknown" вместо пустой метки.
	return label
}

// clientInfoOf reads name/version from _meta first, then from the initialize
// handshake (req.ClientInfo does both). No version — the bare name.
func clientInfoOf(req *mcp.CallToolRequest) string {
	if req == nil {
		return ""
	}
	info := req.ClientInfo()
	if info == nil || info.Name == "" {
		return ""
	}
	if info.Version != "" {
		return info.Name + "/" + info.Version
	}
	return info.Name
}

// identityOf buckets rate limits. Only the key fingerprint identifies a caller:
// it arrives on the context from the transport (stats.WithClientKey), so neither
// the _meta clientInfo nor the User-Agent label — both self-declared — can mint
// fresh quotas or merge two keys into one bucket. An open set sends no key, so
// its caller is bucketed by address, the one thing it cannot claim: behind a
// proxy that address is the proxy's — a coarse divider, not a defence. stdio has
// no address at all and gets one bucket per process.
func identityOf(ctx context.Context) string {
	if fp := stats.ClientKey(ctx); fp != "" {
		return "key:" + fp
	}
	if addr := stats.ClientAddr(ctx); addr != "" {
		return "addr:" + addr
	}
	return "stdin"
}

// throttle refuses a call over the set's rate limit and counts the refusal.
// Nil means the call may proceed; a set without rateLimit never gets past the
// nil limiter, so today's behaviour is unchanged.
func (m *Manager) throttle(ctx context.Context, req *mcp.CallToolRequest, server, tool string) error {
	limits := m.limiter()
	if limits == nil {
		return nil
	}
	ok, perMinute, retry := limits.allow(identityOf(ctx), server, tool)
	if ok {
		return nil
	}
	if m.Stats != nil {
		m.Stats.RecordThrottled(server, tool)
	}
	return fmt.Errorf("лимит %d вызовов в минуту для %s/%s, повторите через %d мс", perMinute, server, tool, retry.Milliseconds())
}

func (m *Manager) find(name string) *entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		if e.server().Name == name {
			return e
		}
	}
	return nil
}

// allEntries copies the entry list: a reload may replace the slice while a
// caller walks it, and the entry pointers themselves stay valid for their
// already-started calls.
func (m *Manager) allEntries() []*entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.entries)
}

// config is the set config the hot path reads: reload swaps it whole.
func (m *Manager) config() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// limiter is the set's rate limiter, nil when the set has none.
func (m *Manager) limiter() *rateLimits {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.limits
}

// dialFor merges the manager context into the dial so Close aborts a connect
// that has not finished yet.
func (m *Manager) dialFor(srv *config.Server) func(context.Context) (*mcp.ClientSession, error) {
	return func(ctx context.Context) (*mcp.ClientSession, error) {
		ctx, span := otel.Tracer(tracerName).Start(ctx, connectSpan)
		defer span.End()
		if span.IsRecording() {
			span.SetAttributes(attribute.String("mcp.server.name", srv.Name))
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(m.ctx, cancel)
		defer stop()
		sess, err := m.dial(ctx, srv)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		return sess, err
	}
}

// callCtx bounds one downstream operation by requestTimeout: a server that
// accepts the connection and stays silent must not hold the caller forever.
func (m *Manager) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if timeout := m.config().RequestTimeout; timeout > 0 {
		return context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	}
	return ctx, func() {}
}

// withSession runs fn on the server's session. A dead cached session
// (ErrConnectionClosed) is closed and dialed once more; a second failure is
// returned with its cause.
func (m *Manager) withSession(ctx context.Context, e *entry, fn func(*mcp.ClientSession) error) error {
	name := e.server().Name
	srv := e.server()
	sess, err := e.conn.session(ctx, m.dialFor(srv), connectDeadline(srv))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	err = fn(sess)
	if !errors.Is(err, mcp.ErrConnectionClosed) {
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	m.dropSession(e)
	srv = e.server()
	sess, err = e.conn.session(ctx, m.dialFor(srv), connectDeadline(srv))
	if err != nil {
		return fmt.Errorf("%s: переподключение после обрыва: %w", name, err)
	}
	if err := fn(sess); err != nil {
		return fmt.Errorf("%s: после переподключения: %w", name, err)
	}
	return nil
}

// callTool runs one call on the cached session. Unlike withSession it never
// retries: invokeCommand decides from the outcome whether a repeat is safe.
func (m *Manager) callTool(ctx context.Context, e *entry, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	srv := e.server()
	sess, err := e.conn.session(ctx, m.dialFor(srv), connectDeadline(srv))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", srv.Name, err)
	}
	// Сервер в песочнице получает свой, более короткий предел: чужая
	// песочница не должна держать вызов столько же, сколько свои серверы.
	parent := ctx
	var limit time.Duration
	if l := srv.CallTimeout(); l > 0 {
		limit = l
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, l)
		defer cancel()
	}
	res, err := sess.CallTool(ctx, params)
	if err != nil {
		// Свой дедлайн отличается от унаследованного (requestTimeout набора
		// или клиента): у родителя ошибки нет, значит истёк именно timeoutSec.
		if limit > 0 && parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			// Зависший вызов остаётся в конвейере сессии — сбрасываем её,
			// иначе следующий вызов получит ответ от прошлого.
			m.dropSession(e)
			return res, fmt.Errorf("%s: таймаут %s (песочница): %w", srv.Name, limit, err)
		}
		return res, fmt.Errorf("%s: %w", srv.Name, err)
	}
	return res, nil
}

// retryableCall reports whether a failed invoke may be repeated. Only a
// transport failure that produced no result qualifies: ErrConnectionClosed and
// a clean EOF mean nothing was read back. io.ErrUnexpectedEOF means the reader
// saw the response start, and any result — including isError — is an answer, so
// neither is repeated. A context error is the caller's deadline, not a drop; a
// net timeout is the same family: the upstream may have run the call, so a
// repeat would double the side effect.
func retryableCall(res *mcp.CallToolResult, err error) bool {
	if err == nil || res != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	if errors.Is(err, mcp.ErrConnectionClosed) || errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && !netErr.Timeout()
}

// Warm connects every enabled server and fills the tool cache, so the first
// client call does not pay for eleven cold starts. Failures are left to the
// regular health probe: warming never blocks the listener.
func (m *Manager) Warm(ctx context.Context) {
	sem := make(chan struct{}, maxProbes)
	var wg sync.WaitGroup
	for _, e := range m.allEntries() {
		if !usable(e.server()) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(e *entry) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(ctx, warmTimeout)
			defer cancel()
			_ = m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
				_, err := m.toolsOf(ctx, e, sess)
				return err
			})
		}(e)
	}
	wg.Wait()
}

// toolsOf returns the server's tools from the cache, fetching them once per
// connection. The cache is dropped when the session is reset.
func (m *Manager) toolsOf(ctx context.Context, e *entry, sess *mcp.ClientSession) ([]*mcp.Tool, error) {
	if cached := e.cachedTools(); cached != nil {
		return cached, nil
	}
	tools, err := listAllTools(ctx, sess)
	if err != nil {
		return nil, err
	}
	m.storeTools(e, tools)
	return tools, nil
}

// listAllTools reads every page, capped so a looping server cannot hang us.
func listAllTools(ctx context.Context, sess *mcp.ClientSession) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	cursor := ""
	for page := 0; page < maxPages; page++ {
		res, err := sess.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" {
			return tools, nil
		}
		cursor = res.NextCursor
	}
	return tools, nil
}

func usable(srv *config.Server) bool { return srv.ConfigError == "" && srv.Enabled }

func serverProblem(srv *config.Server) error {
	if srv.ConfigError != "" {
		return fmt.Errorf("%s: %s", srv.Name, srv.ConfigError)
	}
	return fmt.Errorf("%s: сервер выключен в конфигурации — включите enabled", srv.Name)
}

func transportType(srv *config.Server) string {
	switch srv.Transport {
	case config.TransportStdio:
		return "stdio"
	case config.TransportHTTP:
		return "remote"
	}
	if srv.URL != "" {
		return "remote"
	}
	if len(srv.Command) > 0 {
		return "stdio"
	}
	return ""
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// withAnyOf adds "at least one of these keys" to a schema, so a client that
// validates arguments still accepts either spelling of the same field.
func withAnyOf(s map[string]any, keys ...string) map[string]any {
	alts := make([]any, 0, len(keys))
	for _, k := range keys {
		alts = append(alts, map[string]any{"required": []string{k}})
	}
	s["anyOf"] = alts
	return s
}

// typeErrorText turns a decode type error into one line with the key and the
// expected and actual kinds; scope prefixes the subject, like "calls[0]: ".
func typeErrorText(err error, scope string) (string, bool) {
	var ute *json.UnmarshalTypeError
	if !errors.As(err, &ute) {
		return "", false
	}
	name := scope + "аргументы"
	if ute.Field != "" {
		name = scope + "ключ " + ute.Field
	}
	return fmt.Sprintf("%s: ожидался тип «%s», получен тип «%s»", name, typeKind(ute.Type), valueKind(ute.Value)), true
}

// strictArgs rejects unknown top-level keys before decoding into v. The hint,
// when set, tells the caller where those keys belong instead.
func strictArgs(req *mcp.CallToolRequest, v any, hint string, allowed ...string) error {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return nil
	}
	extra, err := extraKeys(req.Params.Arguments, allowed...)
	if err != nil {
		if text, ok := typeErrorText(err, ""); ok {
			return errors.New(text)
		}
		return fmt.Errorf("аргументы: %w", err)
	}
	if len(extra) > 0 {
		message := "лишние ключи: " + strings.Join(extra, ", ") + " — допустимые: " + strings.Join(allowed, ", ")
		if hint != "" {
			message += "; " + hint
		}
		return errors.New(message)
	}
	if err := json.Unmarshal(req.Params.Arguments, v); err != nil {
		if text, ok := typeErrorText(err, ""); ok {
			return errors.New(text)
		}
		return fmt.Errorf("аргументы: %w", err)
	}
	return nil
}

// typeKind/valueKind translate json.UnmarshalTypeError into the words a caller
// can act on instead of leaking Go struct and type names.
var kindWords = map[reflect.Kind]string{
	reflect.String: "строка",
	reflect.Bool:   "логическое",
	reflect.Slice:  "массив",
	reflect.Map:    "объект",
	reflect.Struct: "объект",
}

var valueWords = map[string]string{
	"string": "строка",
	"bool":   "логическое",
	"array":  "массив",
	"object": "объект",
	"number": "число",
}

func typeKind(t reflect.Type) string {
	k := t.Kind()
	if k >= reflect.Int && k <= reflect.Uintptr {
		return "целое число"
	}
	if word, ok := kindWords[k]; ok {
		return word
	}
	return t.String()
}

func valueKind(v string) string {
	if word, ok := valueWords[v]; ok {
		return word
	}
	return v
}

// extraKeys lists sorted top-level keys of a JSON object that are not allowed.
func extraKeys(raw json.RawMessage, allowed ...string) ([]string, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, err
	}
	var extra []string
	for key := range keys {
		if !slices.Contains(allowed, key) {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	return extra, nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil
}

// jsonTruncResult is jsonResult under maxBytes when the caller asked for a cap:
// a cut JSON catalog is not valid JSON, so an absent or zero maxBytes means no
// cap at all, exactly as the tool behaved before the parameter existed.
func (m *Manager) jsonTruncResult(v any, maxBytes int) (*mcp.CallToolResult, error) {
	res, err := jsonResult(v)
	if err != nil || maxBytes <= 0 {
		return res, err
	}
	return m.truncateResult(res, maxBytes)
}

// resultMessage берёт текст ошибки из ответа инструмента.
func resultMessage(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok && t.Text != "" {
			return t.Text
		}
	}
	return "сервер вернул ошибку без текста — смотрите лог набора"
}

func errResult(err error) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
	}, nil
}

// ─── one session per server, single-flight ────────────────────────────────────

// conn keeps one downstream session per server. A concurrent first call shares
// the in-flight connect; a failed attempt is retried on the next call.
type conn struct {
	mu     sync.Mutex
	sess   *mcp.ClientSession
	try    *attempt
	closed bool
	// retired marks a conn whose entry a reload dropped: the session it holds
	// belongs to the old config, so no new dial may use it.
	retired bool
}

type attempt struct {
	done chan struct{}
	sess *mcp.ClientSession
	err  error
}

// connectDeadline picks the budget for one connect: a server with an oauth
// block may wait for a human, while a plain server should not hold the caller
// for minutes when its initialize hangs.
func connectDeadline(srv *config.Server) time.Duration {
	if srv.OAuth != nil {
		return oauthConnectTimeout
	}
	return connectTimeout
}

func (c *conn) session(ctx context.Context, dial func(context.Context) (*mcp.ClientSession, error), timeout time.Duration) (*mcp.ClientSession, error) {
	c.mu.Lock()
	if c.retired {
		c.mu.Unlock()
		return nil, errRetired
	}
	if c.closed {
		c.mu.Unlock()
		return nil, errClosed
	}
	if c.sess != nil {
		s := c.sess
		c.mu.Unlock()
		return s, nil
	}
	if a := c.try; a != nil {
		c.mu.Unlock()
		select {
		case <-a.done:
			return a.sess, a.err
		case <-ctx.Done():
			if err := c.stopErr(); err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		}
	}
	a := &attempt{done: make(chan struct{})}
	c.try = a
	c.mu.Unlock()

	// The connect belongs to the manager, not to the caller: cancelling one
	// waiting request must not abort a connect another request is waiting on.
	// The deadline does belong to the connect: a hung initialize would otherwise
	// hold c.try forever and make the server look alive.
	go func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		sess, err := dial(dctx)
		timedOut := dctx.Err() == context.DeadlineExceeded
		cancel()
		if timedOut {
			// SDK closes the connection on a failed initialize, so no child is
			// left behind; close anyway in case dial returned a live session.
			if sess != nil {
				_ = sess.Close()
				sess = nil
			}
			if err == nil {
				err = context.DeadlineExceeded
			}
			err = fmt.Errorf("подключение не уложилось в %s: %w", timeout, err)
		}
		c.mu.Lock()
		if stop := c.stopErrLocked(); stop != nil {
			a.sess, a.err = nil, stop
		} else {
			a.sess, a.err = sess, err
			if err == nil {
				c.sess = sess
			}
		}
		stop := c.stopErrLocked()
		c.try = nil
		c.mu.Unlock()
		close(a.done)
		if stop != nil && sess != nil {
			_ = sess.Close()
		}
	}()

	select {
	case <-a.done:
	case <-ctx.Done():
		if err := c.stopErr(); err != nil {
			return nil, err
		}
		return nil, ctx.Err()
	}
	return a.sess, a.err
}

// stopErr names why the conn refuses work — nil while it is live, errRetired
// for an entry a reload dropped, errClosed after Close. A caller woken by the
// manager's context cancel learns the real reason through it.
func (c *conn) stopErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopErrLocked()
}

func (c *conn) stopErrLocked() error {
	switch {
	case c.retired:
		return errRetired
	case c.closed:
		return errClosed
	}
	return nil
}

// markRetired refuses new work on a dropped entry without touching the session
// of a call already inside it.
func (c *conn) markRetired() {
	c.mu.Lock()
	c.retired = true
	c.mu.Unlock()
}

// markClosed makes new and waiting callers see errClosed. Idempotent.
func (c *conn) markClosed() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

// reset drops the cached session; the next call dials again.
func (c *conn) reset() {
	c.mu.Lock()
	s := c.sess
	c.sess = nil
	c.mu.Unlock()
	if s != nil {
		_ = s.Close()
	}
}

// close marks the conn closed, waits at most closeWait for an in-flight
// connect and closes the session.
func (c *conn) close() error {
	c.markClosed()
	c.mu.Lock()
	a := c.try
	c.mu.Unlock()
	if a != nil {
		select {
		case <-a.done:
		case <-time.After(closeWait):
			// Коннект не подтвердил завершение: молча вернуть nil значит
			// соврать вызывающему, что всё закрыто.
			return fmt.Errorf("закрытие: подключение не завершилось за %s", closeWait)
		}
	}
	c.mu.Lock()
	s := c.sess
	c.sess = nil
	c.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

// isolationNote описывает состояние изоляции: применённая песочница или
// причина, по которой сервер работает без неё.
func (e *entry) isolationNote() string {
	srv := e.server()
	if srv.ConfigError != "" {
		// Отвергнутый конфиг не запускается: обещать ему песочницу нельзя.
		return "нет: конфиг отвергнут"
	}
	e.sandboxMu.Lock()
	note := e.sandboxNote
	e.sandboxMu.Unlock()
	if note != "" {
		return "без изоляции: " + note
	}
	if sb := srv.Sandbox; sb != nil && sb.Mode == config.SandboxModeBwrap {
		return "bwrap"
	}
	return ""
}
