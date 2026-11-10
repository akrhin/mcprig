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
	"net"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/downstream"
	"github.com/sintez/mcprig/internal/stats"
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
	// closeWait bounds how long Close waits for an in-flight connect.
	closeWait = 5 * time.Second
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

var (
	errClosed    = errors.New("менеджер закрыт")
	errNoTargets = errors.New("нужен параметр server или servers")
	// errProbeSkipped — проба вообще не началась (не дождалась слота): исход
	// неизвестен, это не отказ сервера.
	errProbeSkipped = errors.New("проба не началась: не дождалась свободного слота")
)

// probeSlots bounds how many servers one list_servers probes at once.
var probeSlots = make(chan struct{}, maxProbes)

// dialer opens one downstream session. Tests point it at in-memory transports.
type dialer func(context.Context, *config.Server) (*mcp.ClientSession, error)

// Manager owns the downstream sessions of one set.
type Manager struct {
	cfg     *config.Config
	version string
	dial    dialer
	entries []*entry

	ctx    context.Context
	cancel context.CancelFunc

	// Stats, when set, counts every downstream invocation per client and
	// tool. The panel reads the recorder; the proxy works without it.
	Stats *stats.Recorder
}

type entry struct {
	srv  *config.Server
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

// hasTool reports whether the cached list names this tool.
func hasTool(tools []*mcp.Tool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func (m *Manager) storeTools(e *entry, tools []*mcp.Tool) {
	e.toolsMu.Lock()
	e.tools = tools
	e.toolsMu.Unlock()
	m.publishSchema()
}

// dropSession closes the cached session and the tool list that came with it.
func (m *Manager) dropSession(e *entry) {
	e.conn.reset()
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
	out := make(map[string][]commandInfo, len(m.entries))
	tools := 0
	for _, e := range m.entries {
		cached := e.cachedTools()
		if len(cached) == 0 {
			continue
		}
		cmds := make([]commandInfo, 0, len(cached))
		for _, tool := range cached {
			cmds = append(cmds, commandInfo{Server: e.srv.Name, Name: tool.Name, Command: tool.Name, Description: tool.Description})
		}
		out[e.srv.Name] = cmds
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
		conn = &downstream.Connector{Version: version}
	}
	if conn.Version == "" {
		conn.Version = version
	}
	m := &Manager{
		cfg:     cfg,
		version: version,
		dial: func(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
			return conn.Connect(ctx, srv)
		},
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	for _, srv := range cfg.Servers {
		m.entries = append(m.entries, &entry{srv: srv, secrets: serverSecrets(srv)})
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
	m.cancel()
	var errs []error
	for _, e := range m.entries {
		if err := e.conn.close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Server builds the MCP server carrying the meta-tools.
func (m *Manager) Server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "mcprig", Version: m.version}, nil)
	s.AddTool(&mcp.Tool{
		Name: "list_servers",
		Description: "List all available MCP servers with their capabilities and tags. " +
			"Use the filter parameter to search by name, description, or tag. " +
			"Includes health status for each server.",
		InputSchema: objectSchema(map[string]any{
			"filter": map[string]any{"type": "string", "description": "Optional: filter servers by name, description, or tag keyword"},
		}),
	}, m.listServers)
	s.AddTool(&mcp.Tool{
		Name: "list_commands",
		Description: "List commands from one server (server) or several at once (servers). " +
			"One of server or servers is required. Use prefix to keep only tool names starting with it.",
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
		}),
	}, m.listCommands)
	s.AddTool(&mcp.Tool{
		Name:        "describe_commands",
		Description: "Get detailed schemas for specific commands from a server",
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
		}, "server", "command_names"),
	}, m.describeCommands)
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
			"Ответ — массив в порядке calls; ошибка одного вызова не валит остальные.",
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
	return s
}

// ─── list_servers ─────────────────────────────────────────────────────────────

type listServersInput struct {
	Filter string `json:"filter,omitempty"`
}

type serverInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Enabled     bool     `json:"enabled"`
	Healthy     bool     `json:"healthy"`
	Error       string   `json:"error,omitempty"`
	Tags        []string `json:"tags,omitempty"`
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
	targets := m.entries
	if filter := strings.ToLower(in.Filter); filter != "" {
		targets = nil
		for _, e := range m.entries {
			if matches(e.srv, filter) {
				targets = append(targets, e)
			}
		}
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
			healthy, errText := m.status(ctx, e)
			out.Servers[i] = serverInfo{
				Name:        e.srv.Name,
				Description: e.srv.Description,
				Type:        transportType(e.srv),
				Enabled:     e.srv.Enabled,
				Healthy:     healthy,
				Error:       errText,
				Tags:        e.srv.Tags,
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
// switched off on purpose is not unhealthy: nothing is dialed for it.
func (m *Manager) status(ctx context.Context, e *entry) (bool, string) {
	if e.srv.ConfigError != "" {
		return false, e.srv.ConfigError
	}
	if !e.srv.Enabled {
		return true, ""
	}
	e.healthMu.Lock()
	h := e.health
	e.healthMu.Unlock()
	if !h.at.IsZero() && time.Since(h.at) < healthTTL {
		return h.ok, h.errText
	}

	var ok bool
	var errText string
	err := m.probe(ctx, e)
	if errors.Is(err, errProbeSkipped) {
		// Исход неизвестен (проба не началась) — не кэшируем и не считаем больным.
		return true, ""
	}
	if err != nil {
		errText = err.Error()
	} else {
		ok = true
	}
	e.healthMu.Lock()
	e.health = healthState{at: time.Now(), ok: ok, errText: errText}
	e.healthMu.Unlock()
	return ok, errText
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
	_, err := e.conn.session(probeCtx, m.dialFor(e.srv))
	return err
}

// ─── list_commands ────────────────────────────────────────────────────────────

type listCommandsInput struct {
	Server    string   `json:"server,omitempty"`
	Servers   []string `json:"servers,omitempty"`
	Prefix    string   `json:"prefix,omitempty"`
	Refresh   bool     `json:"refresh,omitempty"`
	NamesOnly bool     `json:"namesOnly,omitempty"`
}

type commandInfo struct {
	Server      string `json:"server"`
	Name        string `json:"name"`
	Command     string `json:"command"` // kept for older callers; same as name
	Description string `json:"description,omitempty"`
}

func (m *Manager) listCommands(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in listCommandsInput
	if err := strictArgs(req, &in, "", "server", "servers", "prefix", "refresh", "namesOnly"); err != nil {
		return errResult(err)
	}
	if in.Refresh {
		for _, e := range m.entries {
			m.storeTools(e, nil)
		}
	}
	switch {
	case in.Server != "" && len(in.Servers) > 0:
		return errResult(errors.New("укажи server или servers, не оба"))
	case in.Server != "":
		cmds, err := m.commandsOf(ctx, in.Server)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(filterCommands(cmds, in.Prefix, in.NamesOnly))
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
				out[name] = filterCommands(cmds, in.Prefix, in.NamesOnly)
			}(name)
		}
		wg.Wait()
		if failed == len(in.Servers) {
			return errResult(firstErr)
		}
		return jsonResult(out)
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
	if !usable(e.srv) {
		return nil, serverProblem(e.srv)
	}
	if tools := e.cachedTools(); tools != nil {
		out := make([]commandInfo, 0, len(tools))
		for _, tool := range tools {
			out = append(out, commandInfo{Server: name, Name: tool.Name, Command: tool.Name, Description: tool.Description})
		}
		return out, nil
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
	out := make([]commandInfo, 0, len(tools))
	for _, tool := range tools {
		out = append(out, commandInfo{Server: name, Name: tool.Name, Command: tool.Name, Description: tool.Description})
	}
	return out, nil
}

// ─── describe_commands ────────────────────────────────────────────────────────

type describeCommandsInput struct {
	Server       string   `json:"server"`
	CommandNames []string `json:"command_names"`
	Refresh      bool     `json:"refresh,omitempty"`
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
	if err := strictArgs(req, &in, "", "server", "command_names", "refresh"); err != nil {
		return errResult(err)
	}
	if in.Server == "" {
		return errResult(errors.New("server обязателен"))
	}
	e := m.find(in.Server)
	if e == nil {
		return errResult(fmt.Errorf("неизвестный сервер %q", in.Server))
	}
	if !usable(e.srv) {
		return errResult(serverProblem(e.srv))
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
	return jsonResult(out)
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
		return errResult(errors.New("укажите одно: command_name или command"))
	}
	if in.Server == "" {
		return errResult(errors.New("server обязателен"))
	}
	if in.CommandName == "" {
		return errResult(errors.New("укажите command_name или command"))
	}
	maxBytes, err := resolveMaxBytes(in.MaxBytes)
	if err != nil {
		return errResult(err)
	}
	e := m.find(in.Server)
	if e == nil {
		return errResult(fmt.Errorf("неизвестный сервер %q", in.Server))
	}
	if !usable(e.srv) {
		return errResult(serverProblem(e.srv))
	}
	if message, denied := denyReason(m.cfg.Permissions, e.srv.Permissions, e.srv.Name, in.CommandName); denied {
		return blockedResult(e.srv.Name, in.CommandName, message), nil
	}
	// Кэш инструментов уже есть — неизвестное имя не идёт в апстрим: иначе
	// отказ приходит тремя разными текстами (go-sdk, transport, апстрим).
	// Пустой кэш — первый вызов после старта, проверять не по чему.
	if cached := e.cachedTools(); cached != nil && !hasTool(cached, in.CommandName) {
		return errResult(fmt.Errorf("неизвестный инструмент %q у сервера %q", in.CommandName, e.srv.Name))
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
			return errResult(fmt.Errorf("таймаут вызова %d мс", m.cfg.RequestTimeout))
		}
		return errResult(err)
	}
	if res == nil {
		return res, nil
	}
	return truncateResult(res, maxBytes)
}

// callWithRetry runs one downstream call under invoke_command's retry rule and
// counts the outcome. Only a drop before the first response byte is repeated,
// so a side effect is never performed twice.
func (m *Manager) callWithRetry(ctx context.Context, label string, e *entry, name string, args any) (*mcp.CallToolResult, error) {
	params := &mcp.CallToolParams{Name: name, Arguments: args}
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
		m.Stats.Record(label, e.srv.Name, name, time.Since(start), recErr)
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

// labelOf names the harness behind a call. A per-request clientInfo
// (_meta["io.modelcontextprotocol/clientInfo"], SEP-2575) wins: a stateless
// HTTP caller sends it on every request and has no initialize handshake.
// Without it the transport label stands — key fingerprint plus User-Agent on
// HTTP — and only a caller that names itself nowhere is "unknown".
func labelOf(ctx context.Context, req *mcp.CallToolRequest) string {
	if who := clientInfoOf(req); who != "" {
		return who
	}
	return stats.ClientFrom(ctx, "unknown")
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

func (m *Manager) find(name string) *entry {
	for _, e := range m.entries {
		if e.srv.Name == name {
			return e
		}
	}
	return nil
}

// dialFor merges the manager context into the dial so Close aborts a connect
// that has not finished yet.
func (m *Manager) dialFor(srv *config.Server) func(context.Context) (*mcp.ClientSession, error) {
	return func(ctx context.Context) (*mcp.ClientSession, error) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(m.ctx, cancel)
		defer stop()
		return m.dial(ctx, srv)
	}
}

// callCtx bounds one downstream operation by requestTimeout: a server that
// accepts the connection and stays silent must not hold the caller forever.
func (m *Manager) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if m.cfg.RequestTimeout > 0 {
		return context.WithTimeout(ctx, time.Duration(m.cfg.RequestTimeout)*time.Millisecond)
	}
	return ctx, func() {}
}

// withSession runs fn on the server's session. A dead cached session
// (ErrConnectionClosed) is closed and dialed once more; a second failure is
// returned with its cause.
func (m *Manager) withSession(ctx context.Context, e *entry, fn func(*mcp.ClientSession) error) error {
	sess, err := e.conn.session(ctx, m.dialFor(e.srv))
	if err != nil {
		return fmt.Errorf("%s: %w", e.srv.Name, err)
	}
	err = fn(sess)
	if !errors.Is(err, mcp.ErrConnectionClosed) {
		if err != nil {
			return fmt.Errorf("%s: %w", e.srv.Name, err)
		}
		return nil
	}
	m.dropSession(e)
	sess, err = e.conn.session(ctx, m.dialFor(e.srv))
	if err != nil {
		return fmt.Errorf("%s: переподключение после обрыва: %w", e.srv.Name, err)
	}
	if err := fn(sess); err != nil {
		return fmt.Errorf("%s: после переподключения: %w", e.srv.Name, err)
	}
	return nil
}

// callTool runs one call on the cached session. Unlike withSession it never
// retries: invokeCommand decides from the outcome whether a repeat is safe.
func (m *Manager) callTool(ctx context.Context, e *entry, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	sess, err := e.conn.session(ctx, m.dialFor(e.srv))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.srv.Name, err)
	}
	// Сервер в песочнице получает свой, более короткий предел: чужая
	// песочница не должна держать вызов столько же, сколько свои серверы.
	parent := ctx
	var limit time.Duration
	if l := e.srv.CallTimeout(); l > 0 {
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
			return res, fmt.Errorf("%s: таймаут %s (sandbox): %w", e.srv.Name, limit, err)
		}
		return res, fmt.Errorf("%s: %w", e.srv.Name, err)
	}
	return res, nil
}

// retryableCall reports whether a failed invoke may be repeated. Only a
// transport failure that produced no result qualifies: ErrConnectionClosed and
// a clean EOF mean nothing was read back. io.ErrUnexpectedEOF means the reader
// saw the response start, and any result — including isError — is an answer, so
// neither is repeated. A context error is the caller's deadline, not a drop.
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
	return errors.As(err, &netErr)
}

// Warm connects every enabled server and fills the tool cache, so the first
// client call does not pay for eleven cold starts. Failures are left to the
// regular health probe: warming never blocks the listener.
func (m *Manager) Warm(ctx context.Context) {
	sem := make(chan struct{}, maxProbes)
	var wg sync.WaitGroup
	for _, e := range m.entries {
		if !usable(e.srv) {
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
	return fmt.Errorf("%s: выключен", srv.Name)
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

// resultMessage берёт текст ошибки из ответа инструмента.
func resultMessage(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok && t.Text != "" {
			return t.Text
		}
	}
	return "инструмент вернул ошибку"
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
}

type attempt struct {
	done chan struct{}
	sess *mcp.ClientSession
	err  error
}

func (c *conn) session(ctx context.Context, dial func(context.Context) (*mcp.ClientSession, error)) (*mcp.ClientSession, error) {
	c.mu.Lock()
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
			return nil, ctx.Err()
		}
	}
	a := &attempt{done: make(chan struct{})}
	c.try = a
	c.mu.Unlock()

	// The connect belongs to the manager, not to the caller: cancelling one
	// waiting request must not abort a connect another request is waiting on.
	go func() {
		sess, err := dial(context.WithoutCancel(ctx))
		c.mu.Lock()
		if c.closed {
			a.sess, a.err = nil, errClosed
		} else {
			a.sess, a.err = sess, err
			if err == nil {
				c.sess = sess
			}
		}
		closed := c.closed
		c.try = nil
		c.mu.Unlock()
		close(a.done)
		if closed && sess != nil {
			_ = sess.Close()
		}
	}()

	select {
	case <-a.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return a.sess, a.err
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
	c.mu.Lock()
	c.closed = true
	a := c.try
	c.mu.Unlock()
	if a != nil {
		select {
		case <-a.done:
		case <-time.After(closeWait):
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
	if e.srv.ConfigError != "" {
		// Отвергнутый конфиг не запускается: обещать ему песочницу нельзя.
		return "нет: конфиг отвергнут"
	}
	e.sandboxMu.Lock()
	note := e.sandboxNote
	e.sandboxMu.Unlock()
	if note != "" {
		return "без изоляции: " + note
	}
	if sb := e.srv.Sandbox; sb != nil && sb.Mode == config.SandboxModeBwrap {
		return "bwrap"
	}
	return ""
}
