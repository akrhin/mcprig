// Package mcpserver aggregates a set's downstream servers into one MCP server
// exposing lazy-mcp's four meta-tools. Sessions are opened on first use and
// shared by every later call.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcp-manager/internal/config"
	"github.com/sintez/mcp-manager/internal/downstream"
	"github.com/sintez/mcp-manager/internal/stats"
)

const (
	// healthTTL keeps one probe result (including a failure) before re-checking.
	healthTTL = 15 * time.Second
	// healthTimeout bounds one server's status probe.
	healthTimeout = 5 * time.Second
	// closeWait bounds how long Close waits for an in-flight connect.
	closeWait = 5 * time.Second
	// maxPages stops a server that keeps handing out cursors.
	maxPages = 20
	// maxProbes caps concurrent status probes in one list_servers call.
	maxProbes = 8
)

var (
	errClosed    = errors.New("менеджер закрыт")
	errNoTargets = errors.New("нужен параметр server или servers")
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

	healthMu sync.Mutex
	health   healthState
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
		m.entries = append(m.entries, &entry{srv: srv})
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

// Server builds the MCP server carrying the four meta-tools.
func (m *Manager) Server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "mcp-manager", Version: m.version}, nil)
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
			"One of server or servers is required.",
		InputSchema: objectSchema(map[string]any{
			"server": map[string]any{"type": "string", "description": "Server name; returns an array of commands"},
			"servers": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Server names; returns an object keyed by server name",
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
		}, "server", "command_names"),
	}, m.describeCommands)
	s.AddTool(&mcp.Tool{
		Name: "invoke_command",
		Description: "Execute a command from a specific server (auto-discovers if needed). " +
			"This is a wrapper tool: keep only server and command_name at the top level, " +
			"and put downstream tool inputs inside parameters.",
		InputSchema: objectSchema(map[string]any{
			"server":       map[string]any{"type": "string", "description": "Server name"},
			"command_name": map[string]any{"type": "string", "description": "Command name to execute"},
			"parameters": map[string]any{
				"type":        "object",
				"description": "Optional downstream command input object. Put the target command inputs here, not alongside server or command_name.",
			},
		}, "server", "command_name"),
	}, m.invokeCommand)
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
	if err := m.probe(ctx, e); err != nil {
		errText = err.Error()
	} else {
		ok = true
	}
	e.healthMu.Lock()
	e.health = healthState{at: time.Now(), ok: ok, errText: errText}
	e.healthMu.Unlock()
	return ok, errText
}

// probe connects with its own 5 s budget so one hung server cannot stall a list.
func (m *Manager) probe(ctx context.Context, e *entry) error {
	probeCtx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	select {
	case probeSlots <- struct{}{}:
		defer func() { <-probeSlots }()
	case <-probeCtx.Done():
		return probeCtx.Err()
	}
	_, err := e.conn.session(probeCtx, m.dialFor(e.srv))
	return err
}

// ─── list_commands ────────────────────────────────────────────────────────────

type listCommandsInput struct {
	Server  string   `json:"server,omitempty"`
	Servers []string `json:"servers,omitempty"`
}

type commandInfo struct {
	Server      string `json:"server"`
	Name        string `json:"name"`
	Command     string `json:"command"` // kept for older callers; same as name
	Description string `json:"description,omitempty"`
}

func (m *Manager) listCommands(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in listCommandsInput
	if err := strictArgs(req, &in, "", "server", "servers"); err != nil {
		return errResult(err)
	}
	switch {
	case in.Server != "" && len(in.Servers) > 0:
		return errResult(errors.New("укажи server или servers, не оба"))
	case in.Server != "":
		cmds, err := m.commandsOf(ctx, in.Server)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(cmds)
	case len(in.Servers) > 0:
		out := make(map[string][]commandInfo, len(in.Servers))
		for _, name := range in.Servers {
			cmds, err := m.commandsOf(ctx, name)
			if err != nil {
				return errResult(err)
			}
			out[name] = cmds
		}
		return jsonResult(out)
	}
	return errResult(errNoTargets)
}

func (m *Manager) commandsOf(ctx context.Context, name string) ([]commandInfo, error) {
	e := m.find(name)
	if e == nil {
		return nil, fmt.Errorf("неизвестный сервер %q", name)
	}
	if !usable(e.srv) {
		return nil, serverProblem(e.srv)
	}
	var tools []*mcp.Tool
	err := m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		var err error
		tools, err = listAllTools(ctx, sess)
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
	var in describeCommandsInput
	if err := strictArgs(req, &in, "", "server", "command_names"); err != nil {
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
	var tools []*mcp.Tool
	err := m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		var err error
		tools, err = listAllTools(ctx, sess)
		return err
	})
	if err != nil {
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
	Parameters  json.RawMessage `json:"parameters"`
}

func (m *Manager) invokeCommand(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in invokeCommandInput
	if err := strictArgs(req, &in, "аргументы инструмента кладутся в parameters", "server", "command_name", "parameters"); err != nil {
		return errResult(err)
	}
	if in.Server == "" {
		return errResult(errors.New("server обязателен"))
	}
	if in.CommandName == "" {
		return errResult(errors.New("command_name обязателен"))
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

	args := any(map[string]any{})
	if trimmed := strings.TrimSpace(string(in.Parameters)); trimmed != "" && trimmed != "null" {
		if !strings.HasPrefix(trimmed, "{") {
			return errResult(errors.New("parameters: ожидался объект"))
		}
		args = in.Parameters
	}

	callCtx := ctx
	if m.cfg.RequestTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(m.cfg.RequestTimeout)*time.Millisecond)
		defer cancel()
	}
	var res *mcp.CallToolResult
	start := time.Now()
	err := m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		r, err := sess.CallTool(callCtx, &mcp.CallToolParams{Name: in.CommandName, Arguments: args})
		res = r
		return err
	})
	if m.Stats != nil {
		m.Stats.Record(stats.ClientFrom(ctx, clientOf(req)), e.srv.Name, in.CommandName, time.Since(start), err)
	}
	if err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			return errResult(fmt.Errorf("таймаут вызова %d мс", m.cfg.RequestTimeout))
		}
		return errResult(err)
	}
	return res, nil
}

// ─── shared helpers ───────────────────────────────────────────────────────────

// clientOf names the harness behind a call: the initialize handshake carries
// clientInfo (name and version), so any client labels itself. A client that
// sends nothing is "unknown" rather than a guess.
func clientOf(req *mcp.CallToolRequest) string {
	if req == nil || req.Session == nil {
		return "unknown"
	}
	p := req.Session.InitializeParams()
	if p == nil || p.ClientInfo == nil || p.ClientInfo.Name == "" {
		return "unknown"
	}
	if p.ClientInfo.Version != "" {
		return p.ClientInfo.Name + "/" + p.ClientInfo.Version
	}
	return p.ClientInfo.Name
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
	e.conn.reset()
	sess, err = e.conn.session(ctx, m.dialFor(e.srv))
	if err != nil {
		return fmt.Errorf("%s: переподключение после обрыва: %w", e.srv.Name, err)
	}
	if err := fn(sess); err != nil {
		return fmt.Errorf("%s: после переподключения: %w", e.srv.Name, err)
	}
	return nil
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

// strictArgs rejects unknown top-level keys before decoding into v. The hint,
// when set, tells the caller where those keys belong instead.
func strictArgs(req *mcp.CallToolRequest, v any, hint string, allowed ...string) error {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Arguments, &keys); err != nil {
		return fmt.Errorf("аргументы: %w", err)
	}
	var extra []string
	for key := range keys {
		if !slices.Contains(allowed, key) {
			extra = append(extra, key)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		message := "лишние ключи: " + strings.Join(extra, ", ")
		if hint != "" {
			message += " — " + hint
		}
		return errors.New(message)
	}
	if err := json.Unmarshal(req.Params.Arguments, v); err != nil {
		return fmt.Errorf("аргументы: %w", err)
	}
	return nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil
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
