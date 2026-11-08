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
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcp-manager/internal/config"
	"github.com/sintez/mcp-manager/internal/downstream"
)

// dialer opens one downstream session. Tests point it at in-memory transports.
type dialer func(context.Context, *config.Server) (*mcp.ClientSession, error)

// Manager owns the downstream sessions of one set.
type Manager struct {
	cfg     *config.Config
	version string
	dial    dialer
	entries []*entry
}

type entry struct {
	srv  *config.Server
	conn conn
}

// New builds a manager. Nothing is connected until a tool needs a server.
func New(cfg *config.Config, version string) *Manager {
	m := &Manager{
		cfg:     cfg,
		version: version,
		dial: func(ctx context.Context, srv *config.Server) (*mcp.ClientSession, error) {
			return downstream.Connect(ctx, srv, version)
		},
	}
	for _, srv := range cfg.Servers {
		m.entries = append(m.entries, &entry{srv: srv})
	}
	return m
}

// Close shuts every open downstream session.
func (m *Manager) Close() error {
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
		Description: "List commands from a specific server, or from all servers when server is omitted. " +
			"Triggers discovery if not yet loaded.",
		InputSchema: objectSchema(map[string]any{
			"server": map[string]any{"type": "string", "description": "Server name; omit to list every server"},
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
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	Enabled     bool   `json:"enabled"`
	Healthy     bool   `json:"healthy"`
	Error       string `json:"error,omitempty"`
}

func (m *Manager) listServers(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in listServersInput
	if err := decodeArgs(req, &in); err != nil {
		return errResult(err)
	}
	out := struct {
		Servers []serverInfo `json:"servers"`
		PID     int          `json:"pid"`
	}{Servers: make([]serverInfo, len(m.entries)), PID: os.Getpid()}

	var wg sync.WaitGroup
	for i, e := range m.entries {
		wg.Add(1)
		go func(i int, e *entry) {
			defer wg.Done()
			healthy, errText := m.status(ctx, e)
			out.Servers[i] = serverInfo{
				Name:        e.srv.Name,
				Description: e.srv.Description,
				Type:        transportType(e.srv),
				Enabled:     e.srv.Enabled,
				Healthy:     healthy,
				Error:       errText,
			}
		}(i, e)
	}
	wg.Wait()

	if in.Filter != "" {
		filtered := make([]serverInfo, 0, len(out.Servers))
		for _, s := range out.Servers {
			if strings.Contains(strings.ToLower(s.Name+" "+s.Description), strings.ToLower(in.Filter)) {
				filtered = append(filtered, s)
			}
		}
		out.Servers = filtered
	}
	return jsonResult(out)
}

// status reports health; an enabled server is connected on first check.
func (m *Manager) status(ctx context.Context, e *entry) (bool, string) {
	if e.srv.ConfigError != "" {
		return false, e.srv.ConfigError
	}
	if !e.srv.Enabled {
		return false, ""
	}
	if _, err := e.conn.session(ctx, m.dialFor(e.srv)); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// ─── list_commands ────────────────────────────────────────────────────────────

type listCommandsInput struct {
	Server string `json:"server,omitempty"`
}

type commandInfo struct {
	Server      string `json:"server"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

func (m *Manager) listCommands(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in listCommandsInput
	if err := decodeArgs(req, &in); err != nil {
		return errResult(err)
	}
	var targets []*entry
	if in.Server != "" {
		e := m.find(in.Server)
		if e == nil {
			return errResult(fmt.Errorf("неизвестный сервер %q", in.Server))
		}
		if !usable(e.srv) {
			return errResult(serverProblem(e.srv))
		}
		targets = []*entry{e}
	} else {
		for _, e := range m.entries {
			if usable(e.srv) {
				targets = append(targets, e)
			}
		}
	}

	perServer := make([][]commandInfo, len(targets))
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, e := range targets {
		wg.Add(1)
		go func(i int, e *entry) {
			defer wg.Done()
			sess, err := e.conn.session(ctx, m.dialFor(e.srv))
			if err != nil {
				errs[i] = err
				return
			}
			res, err := sess.ListTools(ctx, nil)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", e.srv.Name, err)
				return
			}
			for _, tool := range res.Tools {
				perServer[i] = append(perServer[i], commandInfo{Server: e.srv.Name, Command: tool.Name, Description: tool.Description})
			}
		}(i, e)
	}
	wg.Wait()

	var found error
	for _, err := range errs {
		found = errors.Join(found, err)
	}
	if found != nil {
		return errResult(found)
	}
	out := make([]commandInfo, 0, len(targets))
	for _, cmds := range perServer {
		out = append(out, cmds...)
	}
	return jsonResult(out)
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

func (m *Manager) describeCommands(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in describeCommandsInput
	if err := decodeArgs(req, &in); err != nil {
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
	sess, err := e.conn.session(ctx, m.dialFor(e.srv))
	if err != nil {
		return errResult(err)
	}
	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		return errResult(fmt.Errorf("%s: %w", e.srv.Name, err))
	}
	byName := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	out := make([]commandSchema, 0, len(in.CommandNames))
	for _, name := range in.CommandNames {
		tool := byName[name]
		if tool == nil {
			return errResult(fmt.Errorf("команда %q не найдена на сервере %q", name, in.Server))
		}
		out = append(out, commandSchema{
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
	if err := decodeArgs(req, &in); err != nil {
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
	if len(in.Parameters) > 0 {
		if !strings.HasPrefix(strings.TrimSpace(string(in.Parameters)), "{") {
			return errResult(errors.New("parameters: ожидался объект"))
		}
		args = in.Parameters
	}

	sess, err := e.conn.session(ctx, m.dialFor(e.srv))
	if err != nil {
		return errResult(err)
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: in.CommandName, Arguments: args})
	if err != nil {
		return errResult(fmt.Errorf("%s: %w", e.srv.Name, err))
	}
	return res, nil
}

// ─── shared helpers ───────────────────────────────────────────────────────────

func (m *Manager) find(name string) *entry {
	for _, e := range m.entries {
		if e.srv.Name == name {
			return e
		}
	}
	return nil
}

func (m *Manager) dialFor(srv *config.Server) func(context.Context) (*mcp.ClientSession, error) {
	return func(ctx context.Context) (*mcp.ClientSession, error) { return m.dial(ctx, srv) }
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

func decodeArgs(req *mcp.CallToolRequest, v any) error {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return nil
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
		return nil, errors.New("менеджер закрыт")
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

	sess, err := dial(ctx)
	c.mu.Lock()
	a.sess, a.err = sess, err
	if err == nil {
		c.sess = sess
	}
	c.try = nil
	c.mu.Unlock()
	close(a.done)
	return sess, err
}

func (c *conn) close() error {
	c.mu.Lock()
	c.closed = true
	a := c.try
	s := c.sess
	c.sess = nil
	c.mu.Unlock()
	if a != nil {
		// A connect was in flight; take whatever it produced.
		<-a.done
		c.mu.Lock()
		s = c.sess
		c.sess = nil
		c.mu.Unlock()
	}
	if s == nil {
		return nil
	}
	return s.Close()
}
