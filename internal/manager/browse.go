package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sintez/mcprig/internal/downstream"
	"github.com/sintez/mcprig/internal/registry"
)

const (
	browseTTL       = 15 * time.Minute
	browseMax       = 8
	browseCallLimit = 60 * time.Second
)

var errBrowseGone = errors.New("сессия просмотра не найдена или истекла")

// loggingToolName — инструмент, которым сервер принимает уровень логов.
// Наличие инструмента и есть признак: capability logging deprecated (SEP-2577).
const loggingToolName = "logging/setLevel"

// offersLogging reports whether the server still accepts a log level, judged by
// its tool list rather than the deprecated capability.
func offersLogging(tools []browseTool) bool {
	for _, t := range tools {
		if t.Name == loggingToolName {
			return true
		}
	}
	return false
}

type browseTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema,omitempty"`
}

type browseResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type browsePrompt struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type browseInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type browseView struct {
	ID           string           `json:"id"`
	Server       string           `json:"server"`
	Info         browseInfo       `json:"info"`
	Instructions string           `json:"instructions,omitempty"`
	Capabilities []string         `json:"capabilities"`
	Tools        []browseTool     `json:"tools"`
	Resources    []browseResource `json:"resources"`
	Prompts      []browsePrompt   `json:"prompts"`
	ConnectedAt  string           `json:"connectedAt"`
}

type browseSession struct {
	view *browseView
	sess *mcp.ClientSession
	used time.Time
}

// browser держит открытые MCP-сессии просмотра: не больше 8, TTL 15 минут.
type browser struct {
	mu      sync.Mutex
	items   map[string]*browseSession
	sec     *Secrets
	version string
}

func newBrowser(sec *Secrets, version string) *browser {
	return &browser{items: map[string]*browseSession{}, sec: sec, version: version}
}

func newBrowseID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

func (b *browser) open(ctx context.Context, srv registry.Server) (*browseView, error) {
	b.sweep()
	ctx, cancel := context.WithTimeout(ctx, browseCallLimit)
	defer cancel()

	if msg := srv.Check(); msg != "" {
		return nil, errors.New(msg)
	}
	session, err := (&downstream.Connector{Version: b.version, Env: managerEnv(b.sec)}).Connect(ctx, expandServer(srv, b.sec))
	if err != nil {
		return nil, err
	}
	view := &browseView{
		ID:          newBrowseID(),
		Server:      srv.Name,
		Tools:       []browseTool{},
		Resources:   []browseResource{},
		Prompts:     []browsePrompt{},
		ConnectedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if init := session.InitializeResult(); init != nil {
		if init.ServerInfo != nil {
			view.Info = browseInfo{Name: init.ServerInfo.Name, Version: init.ServerInfo.Version}
		}
		view.Instructions = init.Instructions
		if caps := init.Capabilities; caps != nil {
			if caps.Tools != nil {
				view.Capabilities = append(view.Capabilities, "tools")
			}
			if caps.Resources != nil {
				view.Capabilities = append(view.Capabilities, "resources")
			}
			if caps.Prompts != nil {
				view.Capabilities = append(view.Capabilities, "prompts")
			}
		}
	}
	// Логирование решается по факту, а не по capability: сама capability
	// deprecated с протокола 2026-07-28 (SEP-2577), а сервер, у которого
	// logging/setLevel в списке инструментов, работает.
	if tools, err := session.ListTools(ctx, nil); err == nil {
		for _, t := range tools.Tools {
			view.Tools = append(view.Tools, browseTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
	}
	if offersLogging(view.Tools) {
		view.Capabilities = append(view.Capabilities, "logging")
	}
	if resources, err := session.ListResources(ctx, nil); err == nil {
		for _, r := range resources.Resources {
			view.Resources = append(view.Resources, browseResource{URI: r.URI, Name: r.Name, Description: r.Description, MIMEType: r.MIMEType})
		}
	}
	if prompts, err := session.ListPrompts(ctx, nil); err == nil {
		for _, p := range prompts.Prompts {
			view.Prompts = append(view.Prompts, browsePrompt{Name: p.Name, Description: p.Description})
		}
	}

	b.mu.Lock()
	if len(b.items) >= browseMax {
		b.evictLocked()
	}
	b.items[view.ID] = &browseSession{view: view, sess: session, used: time.Now()}
	b.mu.Unlock()
	return view, nil
}

func (b *browser) lookup(id string) (*browseSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.items[id]
	if !ok {
		return nil, errBrowseGone
	}
	if time.Since(s.used) > browseTTL {
		delete(b.items, id)
		go s.sess.Close()
		return nil, errBrowseGone
	}
	s.used = time.Now()
	return s, nil
}

func (b *browser) get(id string) (*browseView, error) {
	s, err := b.lookup(id)
	if err != nil {
		return nil, err
	}
	return s.view, nil
}

func (b *browser) close(id string) bool {
	b.mu.Lock()
	s, ok := b.items[id]
	delete(b.items, id)
	b.mu.Unlock()
	if ok {
		go s.sess.Close()
	}
	return ok
}

func (b *browser) callTool(ctx context.Context, id, name string, args map[string]any) (any, error) {
	s, err := b.lookup(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, browseCallLimit)
	defer cancel()
	res, err := s.sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (b *browser) readResource(ctx context.Context, id, uri string) (any, error) {
	s, err := b.lookup(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, browseCallLimit)
	defer cancel()
	res, err := s.sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (b *browser) getPrompt(ctx context.Context, id, name string, args map[string]string) (any, error) {
	s, err := b.lookup(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, browseCallLimit)
	defer cancel()
	res, err := s.sess.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (b *browser) sweep() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, s := range b.items {
		if time.Since(s.used) > browseTTL {
			delete(b.items, id)
			go s.sess.Close()
		}
	}
}

// evictLocked закрывает самую старую сессию — вызывается под b.mu.
func (b *browser) evictLocked() {
	oldest := ""
	var oldestAt time.Time
	for id, s := range b.items {
		if oldest == "" || s.used.Before(oldestAt) {
			oldest, oldestAt = id, s.used
		}
	}
	if oldest == "" {
		return
	}
	if s := b.items[oldest]; s != nil {
		go s.sess.Close()
	}
	delete(b.items, oldest)
}

func (b *browser) closeAll() {
	b.mu.Lock()
	ids := make([]string, 0, len(b.items))
	for id := range b.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := b.items
	b.items = map[string]*browseSession{}
	b.mu.Unlock()
	for _, id := range ids {
		if s := items[id]; s != nil {
			s.sess.Close()
		}
	}
}
