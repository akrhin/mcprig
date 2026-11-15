package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/downstream"
	"github.com/akrhin/mcprig/internal/registry"
	"github.com/akrhin/mcprig/internal/toollock"
)

const (
	browseTTL       = 15 * time.Minute
	browseMax       = 8
	browseCallLimit = 60 * time.Second
)

var (
	errBrowseGone        = errors.New("сессия просмотра не найдена или истекла")
	errBrowseDenied      = errors.New("вызов запрещён правилами permissions")
	errBrowseQuarantined = errors.New("вызов ждёт одобрения дескриптора")
)

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
	// Changed — дескриптор разошёлся с одобренным: инструмент виден, вызов
	// ждёт одобрения (тот же карантин, что у invoke_command).
	Changed bool `json:"changed,omitempty"`
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
	// server и perms — из реестра, чтобы callTool применял то же `permissions`,
	// что и invoke_command. Глобального блока у панели нет: он принадлежит
	// набору и сюда не приходит.
	server string
	perms  *config.Permissions
	// tools — дескрипторы как их отдал сервер: по ним сверяется карантин.
	tools map[string]*mcp.Tool
	// refs — сколько вызовов сейчас держат сессию. close и sweep помечают её
	// closing и закрывают, когда refs снова 0: иначе закрытие прилетает в
	// середину вызова.
	refs    int
	closing bool
}

// browser держит открытые MCP-сессии просмотра: не больше 8, TTL 15 минут.
type browser struct {
	mu      sync.Mutex
	items   map[string]*browseSession
	sec     *Secrets
	version string
	// dataDir — где лежат sets/<id>/tools.lock.json. Пустой — карантина нет
	// (ручной serve без набора), как и у invoke_command.
	dataDir string
}

func newBrowser(sec *Secrets, version, dataDir string) *browser {
	return &browser{items: map[string]*browseSession{}, sec: sec, version: version, dataDir: dataDir}
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
	// rawTools — те же дескрипторы целиком: карантин считает хэш по пяти
	// полям, и разложенный browseTool их не сохраняет.
	var rawTools []*mcp.Tool
	if tools, err := session.ListTools(ctx, nil); err == nil {
		rawTools = tools.Tools
		for _, t := range tools.Tools {
			_, changed := b.quarantine(srv.Name, t)
			view.Tools = append(view.Tools, browseTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, Changed: changed})
		}
	}
	byName := make(map[string]*mcp.Tool, len(rawTools))
	for _, t := range rawTools {
		byName[t.Name] = t
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
	var victim *browseSession
	if len(b.items) >= browseMax {
		victim = b.evictLocked()
	}
	b.items[view.ID] = &browseSession{view: view, sess: session, used: time.Now(), server: srv.Name, perms: srv.Permissions, tools: byName}
	b.mu.Unlock()
	if victim != nil {
		go victim.sess.Close()
	}
	return view, nil
}

// lookup берёт сессию на время вызова: refs++ не даёт close/sweep закрыть её
// посреди работы. Пара к нему — release.
func (b *browser) lookup(id string) (*browseSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.items[id]
	if !ok || s.closing {
		return nil, errBrowseGone
	}
	if s.refs == 0 && time.Since(s.used) > browseTTL {
		delete(b.items, id)
		s.closing = true
		go s.sess.Close()
		return nil, errBrowseGone
	}
	s.used = time.Now()
	s.refs++
	return s, nil
}

// release отпускает сессию; закрывается она здесь, если её уже пометили.
func (b *browser) release(s *browseSession) {
	b.mu.Lock()
	s.refs--
	closeNow := s.closing && s.refs <= 0
	b.mu.Unlock()
	if closeNow {
		s.sess.Close()
	}
}

func (b *browser) get(id string) (*browseView, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.items[id]
	if !ok || s.closing {
		return nil, errBrowseGone
	}
	if s.refs == 0 && time.Since(s.used) > browseTTL {
		delete(b.items, id)
		s.closing = true
		go s.sess.Close()
		return nil, errBrowseGone
	}
	s.used = time.Now()
	return s.view, nil
}

func (b *browser) close(id string) bool {
	b.mu.Lock()
	s, ok := b.items[id]
	if ok {
		delete(b.items, id)
		s.closing = true
	}
	closeNow := ok && s.refs == 0
	b.mu.Unlock()
	if closeNow {
		go s.sess.Close()
	}
	return ok
}

// deny is the browse gate: the server's own `permissions` rules decide, the
// same way invoke_command decides for the proxy path, and the address is the
// same one that path uses ("resource:<uri>", "prompt:<name>"). The panel has no
// global block on purpose — that block belongs to the set.
func (s *browseSession) deny(address string) error {
	message, denied := config.DenyReason(nil, s.perms, s.server, address)
	if !denied {
		return nil
	}
	if message == "" {
		return errBrowseDenied
	}
	return fmt.Errorf("%w: %s", errBrowseDenied, message)
}

func (b *browser) callTool(ctx context.Context, id, name string, args map[string]any) (any, error) {
	s, err := b.lookup(id)
	if err != nil {
		return nil, err
	}
	defer b.release(s)
	if err := s.deny(name); err != nil {
		return nil, err
	}
	// Карантин — после permissions, до вызова: кнопка browse не должна
	// исполнять то, что invoke_command отказался бы исполнить.
	if tool := s.tools[name]; tool != nil {
		if message, changed := b.quarantine(s.server, tool); changed {
			return nil, fmt.Errorf("%w: %s", errBrowseQuarantined, message)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, browseCallLimit)
	defer cancel()
	res, err := s.sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// quarantine — тот же файл одобрений, что читает прокси набора, но наборов
// может быть несколько: сервер принадлежит им всем. Изменённый хоть в одном
// наборе дескриптор останавливает вызов — иначе второй набор был бы лазейкой.
func (b *browser) quarantine(server string, tool *mcp.Tool) (string, bool) {
	if b.dataDir == "" {
		return "", false
	}
	sets, err := os.ReadDir(filepath.Join(b.dataDir, "sets"))
	if err != nil {
		return "", false
	}
	now := toollock.Hash(tool)
	key := toollock.Key(server, tool.Name)
	for _, dir := range sets {
		if !dir.IsDir() {
			continue
		}
		if cur, ok := toollock.Read(toollock.Path(b.dataDir, dir.Name())).Tools[key]; ok && cur.Hash != now {
			return toollock.Message(server, tool.Name, cur.Hash, now, dir.Name()), true
		}
	}
	return "", false
}

func (b *browser) readResource(ctx context.Context, id, uri string) (any, error) {
	s, err := b.lookup(id)
	if err != nil {
		return nil, err
	}
	defer b.release(s)
	if err := s.deny("resource:" + uri); err != nil {
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
	defer b.release(s)
	if err := s.deny("prompt:" + name); err != nil {
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
	now := time.Now()
	var closeNow []*browseSession
	for id, s := range b.items {
		if s.refs > 0 || now.Sub(s.used) <= browseTTL {
			continue
		}
		delete(b.items, id)
		s.closing = true
		closeNow = append(closeNow, s)
	}
	b.mu.Unlock()
	for _, s := range closeNow {
		go s.sess.Close()
	}
}

// evictLocked отдаёт самую старую свободную сессию — вызывается под b.mu, а
// закрывает её open уже после разблокировки. Сессии в работе не вытесняются.
func (b *browser) evictLocked() *browseSession {
	var oldest *browseSession
	oldestID := ""
	for id, s := range b.items {
		if s.refs > 0 {
			continue
		}
		if oldest == nil || s.used.Before(oldest.used) {
			oldest, oldestID = s, id
		}
	}
	if oldest == nil {
		return nil
	}
	delete(b.items, oldestID)
	oldest.closing = true
	return oldest
}

func (b *browser) closeAll() {
	b.mu.Lock()
	items := b.items
	b.items = map[string]*browseSession{}
	var closeNow []*browseSession
	for _, s := range items {
		s.closing = true
		if s.refs == 0 {
			closeNow = append(closeNow, s)
		}
	}
	b.mu.Unlock()
	for _, s := range closeNow {
		s.sess.Close()
	}
}
