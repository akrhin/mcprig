// Resources and prompts of downstream servers, reachable by the MCP client.
// A server contributes them only when its config names them in `expose`:
// without that the client sees tools alone, as before the field existed.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// exposes reports whether the server contributes this kind of surface.
func exposes(srv *config.Server, kind string) bool {
	for _, k := range srv.Expose {
		if k == kind {
			return true
		}
	}
	return false
}

// notExposed answers a call aimed at a surface the set does not carry.
func notExposed(srv *config.Server, kind, what string) error {
	return fmt.Errorf("%s: набор не отдаёт %s — добавьте %q в expose этого сервера", srv.Name, what, kind)
}

type listResourcesInput struct {
	Server   string `json:"server,omitempty"`
	MaxBytes int    `json:"maxBytes,omitempty"`
}

type resourceInfo struct {
	Server      string `json:"server"`
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type readResourceInput struct {
	Server   string `json:"server"`
	URI      string `json:"uri"`
	MaxBytes int    `json:"maxBytes,omitempty"`
}

type listPromptsInput struct {
	Server   string `json:"server,omitempty"`
	MaxBytes int    `json:"maxBytes,omitempty"`
}

type promptInfo struct {
	Server      string         `json:"server"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Arguments   []promptArgOut `json:"arguments,omitempty"`
}

type promptArgOut struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type getPromptInput struct {
	Server    string            `json:"server"`
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
	MaxBytes  int               `json:"maxBytes,omitempty"`
}

// addResourceTools registers the four meta-tools. Kept beside the handlers so
// the tool list of the proxy stays readable in one place.
func (m *Manager) addResourceTools(s *mcp.Server) {
	s.AddTool(&mcp.Tool{
		Name: "list_resources",
		Description: "List resources of servers that expose them (server) or of every such server (no server). " +
			"An answer over maxBytes is cut and the full one is written to a file.",
		InputSchema: objectSchema(map[string]any{
			"server": map[string]any{"type": "string", "description": "Server name; returns an array of resources"},
			"maxBytes": map[string]any{
				"type":        "integer",
				"description": "Optional: cap on the answer in bytes (max 1048576); without it the answer is not cut",
			},
		}),
	}, m.recordMeta("list_resources", m.listResources))
	s.AddTool(&mcp.Tool{
		Name: "read_resource",
		Description: "Read one resource of a server. The answer carries the upstream contents[] with their mimeType, " +
			"cut at maxBytes (default 32768) with the full one written to a file.",
		InputSchema: objectSchema(map[string]any{
			"server": map[string]any{"type": "string", "description": "Server name"},
			"uri":    map[string]any{"type": "string", "description": "Resource URI as list_resources returned it"},
			"maxBytes": map[string]any{
				"type":        "integer",
				"description": "Optional: cap the returned answer at this many bytes (default 32768, at most 1048576)",
			},
		}, "server", "uri"),
	}, m.recordMeta("read_resource", m.readResource))
	s.AddTool(&mcp.Tool{
		Name: "list_prompts",
		Description: "List prompts of servers that expose them (server) or of every such server (no server). " +
			"An answer over maxBytes is cut and the full one is written to a file.",
		InputSchema: objectSchema(map[string]any{
			"server": map[string]any{"type": "string", "description": "Server name; returns an array of prompts"},
			"maxBytes": map[string]any{
				"type":        "integer",
				"description": "Optional: cap on the answer in bytes (max 1048576); without it the answer is not cut",
			},
		}),
	}, m.recordMeta("list_prompts", m.listPrompts))
	s.AddTool(&mcp.Tool{
		Name:        "get_prompt",
		Description: "Render one prompt of a server with arguments; the upstream messages come back unchanged.",
		InputSchema: objectSchema(map[string]any{
			"server": map[string]any{"type": "string", "description": "Server name"},
			"name":   map[string]any{"type": "string", "description": "Prompt name"},
			"arguments": map[string]any{
				"type":                 "object",
				"description":          "Optional prompt arguments as name → value",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"maxBytes": map[string]any{
				"type":        "integer",
				"description": "Optional: cap the returned answer at this many bytes (default 32768, at most 1048576)",
			},
		}, "server", "name"),
	}, m.recordMeta("get_prompt", m.getPrompt))
}

func (m *Manager) listResources(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in listResourcesInput
	if err := strictArgs(req, &in, "", "server", "maxBytes"); err != nil {
		return errResult(err)
	}
	maxBytes := 0
	// 0 — поле не задано, ответ не режется; отрицательное значение — ошибка,
	// а не «без потолка» (resolveMaxBytes её и называет).
	if in.MaxBytes != 0 {
		var err error
		if maxBytes, err = resolveMaxBytes(in.MaxBytes); err != nil {
			return errResult(err)
		}
	}
	ctx, cancel := m.callCtx(ctx)
	defer cancel()
	if in.Server != "" {
		e := m.find(in.Server)
		if e == nil {
			return errResult(fmt.Errorf("неизвестный сервер %q", in.Server))
		}
		if srv := e.server(); !usable(srv) {
			return errResult(serverProblem(srv))
		} else if !exposes(srv, config.ExposeResources) {
			return errResult(notExposed(srv, config.ExposeResources, "ресурсы"))
		} else if err := m.throttle(ctx, req, srv.Name, "list_resources"); err != nil {
			return errResult(err)
		}
		items, err := m.resourcesOf(ctx, e)
		if err != nil {
			return errResult(err)
		}
		return m.jsonTruncResult(ctx, items, maxBytes)
	}
	names := make([]string, 0, len(m.allEntries()))
	for _, e := range m.allEntries() {
		if srv := e.server(); usable(srv) && exposes(srv, config.ExposeResources) {
			names = append(names, srv.Name)
		}
	}
	return m.jsonTruncResult(ctx, collect(m, ctx, names, func(ctx context.Context, e *entry) ([]resourceInfo, error) {
		if err := m.throttle(ctx, req, e.server().Name, "list_resources"); err != nil {
			return nil, err
		}
		return m.resourcesOf(ctx, e)
	}), maxBytes)
}

func (m *Manager) readResource(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in readResourceInput
	if err := strictArgs(req, &in, "", "server", "uri", "maxBytes"); err != nil {
		return errResult(err)
	}
	if in.Server == "" || in.URI == "" {
		return errResult(errors.New("server и uri обязательны"))
	}
	maxBytes, err := resolveMaxBytes(in.MaxBytes)
	if err != nil {
		return errResult(err)
	}
	ctx, cancel := m.callCtx(ctx)
	defer cancel()
	e, err := m.exposedEntry(ctx, req, in.Server, config.ExposeResources, "ресурсы", "read_resource", "resource:"+in.URI)
	if err != nil {
		return resOrErr(err)
	}
	var contents []*mcp.ResourceContents
	err = m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		res, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: in.URI, Meta: traceMeta(ctx)})
		if err != nil {
			return fmt.Errorf("%s: ресурс %s: %w", in.Server, in.URI, err)
		}
		contents = res.Contents
		return nil
	})
	if err != nil {
		return errResult(err)
	}
	// contents[] уходят как есть: клиент получает uri, mimeType, text или blob
	// в том виде, в каком их отдал upstream.
	res, err := jsonResult(contents)
	if err != nil {
		return res, err
	}
	return m.truncateResult(ctx, scrubSecrets(res, e.secrets), maxBytes)
}

func (m *Manager) listPrompts(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in listPromptsInput
	if err := strictArgs(req, &in, "", "server", "maxBytes"); err != nil {
		return errResult(err)
	}
	maxBytes := 0
	// 0 — поле не задано, ответ не режется; отрицательное значение — ошибка.
	if in.MaxBytes != 0 {
		var err error
		if maxBytes, err = resolveMaxBytes(in.MaxBytes); err != nil {
			return errResult(err)
		}
	}
	ctx, cancel := m.callCtx(ctx)
	defer cancel()
	if in.Server != "" {
		e := m.find(in.Server)
		if e == nil {
			return errResult(fmt.Errorf("неизвестный сервер %q", in.Server))
		}
		if srv := e.server(); !usable(srv) {
			return errResult(serverProblem(srv))
		} else if !exposes(srv, config.ExposePrompts) {
			return errResult(notExposed(srv, config.ExposePrompts, "промпты"))
		} else if err := m.throttle(ctx, req, srv.Name, "list_prompts"); err != nil {
			return errResult(err)
		}
		items, err := m.promptsOf(ctx, e)
		if err != nil {
			return errResult(err)
		}
		return m.jsonTruncResult(ctx, items, maxBytes)
	}
	names := make([]string, 0, len(m.allEntries()))
	for _, e := range m.allEntries() {
		if srv := e.server(); usable(srv) && exposes(srv, config.ExposePrompts) {
			names = append(names, srv.Name)
		}
	}
	return m.jsonTruncResult(ctx, collect(m, ctx, names, func(ctx context.Context, e *entry) ([]promptInfo, error) {
		if err := m.throttle(ctx, req, e.server().Name, "list_prompts"); err != nil {
			return nil, err
		}
		return m.promptsOf(ctx, e)
	}), maxBytes)
}

func (m *Manager) getPrompt(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in getPromptInput
	if err := strictArgs(req, &in, "", "server", "name", "arguments", "maxBytes"); err != nil {
		return errResult(err)
	}
	if in.Server == "" || in.Name == "" {
		return errResult(errors.New("server и name обязательны"))
	}
	maxBytes, err := resolveMaxBytes(in.MaxBytes)
	if err != nil {
		return errResult(err)
	}
	ctx, cancel := m.callCtx(ctx)
	defer cancel()
	e, err := m.exposedEntry(ctx, req, in.Server, config.ExposePrompts, "промпты", "get_prompt", "prompt:"+in.Name)
	if err != nil {
		return resOrErr(err)
	}
	var out *mcp.GetPromptResult
	err = m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		var err error
		out, err = sess.GetPrompt(ctx, &mcp.GetPromptParams{Name: in.Name, Arguments: in.Arguments, Meta: traceMeta(ctx)})
		if err != nil {
			return fmt.Errorf("%s: промпт %s: %w", in.Server, in.Name, err)
		}
		return nil
	})
	if err != nil {
		return errResult(err)
	}
	res, err := jsonResult(out)
	if err != nil {
		return res, err
	}
	return m.truncateResult(ctx, scrubSecrets(res, e.secrets), maxBytes)
}

// exposedEntry does what invoke_command does before a session: find, usable,
// expose, throttle and the permission gate. Throttle counts the meta-tool (one
// bucket per server, not per URI — a filesystem server has thousands of URIs);
// the permission "command" is the addressed name ("resource:<uri>",
// "prompt:<name>"), so the ordinary rule vocabulary needs no new kind.
func (m *Manager) exposedEntry(ctx context.Context, req *mcp.CallToolRequest, serverName, kind, what, target, addressed string) (*entry, error) {
	e := m.find(serverName)
	if e == nil {
		return nil, fmt.Errorf("неизвестный сервер %q", serverName)
	}
	srv := e.server()
	if !usable(srv) {
		return nil, serverProblem(srv)
	}
	if !exposes(srv, kind) {
		return nil, notExposed(srv, kind, what)
	}
	if err := m.throttle(ctx, req, srv.Name, target); err != nil {
		return nil, err
	}
	if message, denied := m.gate(ctx, srv.Name, addressed, srv.Permissions); denied {
		return nil, blockedError{blockedResult(srv.Name, addressed, message)}
	}
	return e, nil
}

// blockedError carries a ready policy answer through the error path, so the
// gate's "🚫 Blocked by mcprig policy" shape is what a denied call returns.
type blockedError struct{ res *mcp.CallToolResult }

func (b blockedError) Error() string { return resultMessage(b.res) }

func resOrErr(err error) (*mcp.CallToolResult, error) {
	var blocked blockedError
	if errors.As(err, &blocked) {
		return blocked.res, nil
	}
	return errResult(err)
}

// targetAllowed applies to a listed item the policy read_resource and get_prompt
// apply to the read: an address the caller may not reach is not shown. The URI
// and the name come from the listing itself, so filtering by the addressed name
// works here the same way it does on the read.
func (m *Manager) targetAllowed(ctx context.Context, e *entry, addressed string) bool {
	srv := e.server()
	_, denied := m.gate(ctx, srv.Name, addressed, srv.Permissions)
	return !denied
}

// resourcesOf reads one server's resource list, following cursors like toolsOf.
func (m *Manager) resourcesOf(ctx context.Context, e *entry) ([]resourceInfo, error) {
	out := []resourceInfo{}
	name := e.server().Name
	err := m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		params := &mcp.ListResourcesParams{}
		for page := 0; page < maxPages; page++ {
			res, err := sess.ListResources(ctx, params)
			if err != nil {
				return fmt.Errorf("%s: ресурсы: %w", name, err)
			}
			for _, r := range res.Resources {
				// Тот же гейт, что у read_resource: инвентарь не выдаёт
				// имён, которые чтением не пройдут.
				if !m.targetAllowed(ctx, e, "resource:"+r.URI) {
					continue
				}
				out = append(out, resourceInfo{
					Server: name, URI: r.URI, Name: r.Name, Title: r.Title,
					Description: r.Description, MIMEType: r.MIMEType, Size: r.Size,
				})
			}
			if res.NextCursor == "" {
				return nil
			}
			params.Cursor = res.NextCursor
		}
		return nil
	})
	return out, err
}

// promptsOf reads one server's prompt list.
func (m *Manager) promptsOf(ctx context.Context, e *entry) ([]promptInfo, error) {
	out := []promptInfo{}
	name := e.server().Name
	err := m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
		params := &mcp.ListPromptsParams{}
		for page := 0; page < maxPages; page++ {
			res, err := sess.ListPrompts(ctx, params)
			if err != nil {
				return fmt.Errorf("%s: промпты: %w", name, err)
			}
			for _, p := range res.Prompts {
				// Промпт, который get_prompt отвергнет, в списке не появляется.
				if !m.targetAllowed(ctx, e, "prompt:"+p.Name) {
					continue
				}
				info := promptInfo{Server: name, Name: p.Name, Title: p.Title, Description: p.Description}
				for _, a := range p.Arguments {
					info.Arguments = append(info.Arguments, promptArgOut{Name: a.Name, Description: a.Description, Required: a.Required})
				}
				out = append(out, info)
			}
			if res.NextCursor == "" {
				return nil
			}
			params.Cursor = res.NextCursor
		}
		return nil
	})
	return out, err
}

// collect lists every named server in parallel and keys the answer by server, as
// list_commands does: one failing server keeps its error, not the whole answer.
func collect[T any](m *Manager, ctx context.Context, names []string, list func(context.Context, *entry) ([]T, error)) map[string]any {
	out := make(map[string]any, len(names))
	var mu sync.Mutex
	var firstErr error
	failed := 0
	const parallel = 8
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			var items []T
			var err error
			if e := m.find(name); e != nil {
				items, err = list(ctx, e)
			} else {
				err = fmt.Errorf("неизвестный сервер %q", name)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out[name] = map[string]string{"error": err.Error()}
				failed++
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			out[name] = items
		}(name)
	}
	wg.Wait()
	if failed > 0 && failed == len(names) {
		// Ключ с подчёркиванием: «error» — законное имя сервера, и его отказ
		// не должен затирать сводку.
		out["_errors"] = firstErr.Error()
	}
	return out
}
