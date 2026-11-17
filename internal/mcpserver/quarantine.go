package mcpserver

// Карантин дескрипторов. Первое появление инструмента прокси доверяет сам
// (иначе карантин требовал бы одобрения на каждом новом сервере и его бы
// отключали), а вот смена дескриптора у уже одобренного инструмента значит,
// что подменили реализацию или обновили версию, — такой вызов ждёт человека.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/toollock"
)

// observe записывает дескрипторы, которых в файле ещё нет, и запоминает хэш
// изменившихся. Хэш изменившегося не становится одобренным: иначе карантин
// снимался бы сам при первом же list_commands.
func (m *Manager) observe(e *entry, tools []*mcp.Tool) {
	if m.LockPath == "" || len(tools) == 0 {
		return
	}
	name := e.server().Name
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	// Ошибка записи не рушит каталог, но и не проходит молча: пока файл не
	// записан, карантин не знает, что одобрено, и вызовы не идут (quarantine).
	// Запись пытается каждый следующий обход каталога: восстановленный файл
	// снимает запрет без перезапуска.
	err := toollock.Update(m.LockPath, func(f *toollock.File) bool {
		dirty := false
		for _, t := range tools {
			key := toollock.Key(name, t.Name)
			hash := toollock.Hash(t)
			cur, seen := f.Tools[key]
			switch {
			case !seen:
				f.Tools[key] = toollock.Entry{Hash: hash, ApprovedAt: now, ApprovedBy: "auto"}
				dirty = true
			case cur.Hash == hash && cur.Seen != "":
				// Дескриптор вернулся к одобренному (человек откатил версию
				// сервера): пометка о смене снимается, иначе --all одобрил бы
				// хэш, которого больше нет, и запер рабочий инструмент.
				cur.Seen = ""
				f.Tools[key] = cur
				dirty = true
			case cur.Hash != hash && cur.Seen != hash:
				cur.Seen = hash
				f.Tools[key] = cur
				dirty = true
			}
		}
		return dirty
	})
	if err != nil {
		// Битый файл и несостоявшаяся запись равно значат «одобрений нет»:
		// карантин не может ручаться ни за один дескриптор.
		slog.Error("карантин: tools.lock.json не обновлён — вызовы отклоняются", "path", m.LockPath, "server", name, "err", err)
	}
	m.lockErr = err
}

// quarantine refuses a call whose tool descriptor no longer matches the
// approved hash. The descriptor comes from the server catalog; see toolForCall.
//
// Неизвестное состояние — отказ: файл, который не читается или не записался,
// не даёт отличить одобренный дескриптор от подменённого, а «наверное, всё в
// порядке» здесь означает обход карантина. Отсутствующий файл — не ошибка: у
// нового набора ещё нет записей, и первое появление одобряется само.
func (m *Manager) quarantine(serverName string, tool *mcp.Tool) (string, bool) {
	if m.LockPath == "" {
		return "", false
	}
	m.lockMu.Lock()
	writeErr := m.lockErr
	m.lockMu.Unlock()
	// Файл читается на каждый вызов: он мал (запись на инструмент), а вызов
	// идёт в сеть за десятки миллисекунд. shortcut: кэш по mtime/size — если
	// файл начнёт измеряться сотнями записей.
	f, err := toollock.ReadFile(m.LockPath)
	switch {
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		slog.Error("карантин: tools.lock.json не прочитан — вызов отклонён", "path", m.LockPath, "err", err)
		return lockFailureMessage(serverName, tool.Name, "файл одобрений не прочитан", err), true
	case writeErr != nil:
		// Причина уже в логе (observe); повтор на каждый вызов только засорил бы его.
		return lockFailureMessage(serverName, tool.Name, "файл одобрений не обновлён", writeErr), true
	}
	approved, ok := f.Tools[toollock.Key(serverName, tool.Name)]
	if !ok {
		return "", false
	}
	now := toollock.Hash(tool)
	if approved.Hash == now {
		return "", false
	}
	return quarantineMessage(serverName, tool.Name, approved.Hash, now), true
}

// toolForCall — дескриптор инструмента для проверки карантина и имена, которые
// апстрим не знает. Кэш каталога пустеет штатно (старт набора, refresh,
// notifications/tools/list_changed, обрыв сессии), поэтому при включённом
// карантине каталог читается здесь же, до вызова: пустой кэш не открывает
// обход. Каталог не прочитан — отказ: вслепую вызов не идёт.
func (m *Manager) toolForCall(ctx context.Context, e *entry, srv *config.Server, name string) (*mcp.Tool, error) {
	cached := e.cachedTools()
	if cached == nil && m.LockPath != "" {
		if err := m.withSession(ctx, e, func(sess *mcp.ClientSession) error {
			var err error
			cached, err = m.toolsOf(ctx, e, sess)
			return err
		}); err != nil {
			return nil, fmt.Errorf("карантин: каталог сервера %q не прочитан: %w", srv.Name, err)
		}
	}
	// Без карантина (ручной serve/stdio) пустой кэш — по-прежнему «проверять
	// не по чему»: каталог ради одного имени не поднимаем.
	if cached == nil {
		return nil, nil
	}
	tool := findTool(cached, name)
	if tool == nil {
		return nil, fmt.Errorf("неизвестный инструмент %q у сервера %q", name, srv.Name)
	}
	return tool, nil
}

// changedTools names the cached tools whose descriptor differs from the
// approved hash; list_commands marks them instead of hiding them.
func (m *Manager) changedTools(serverName string, tools []*mcp.Tool) map[string]bool {
	if m.LockPath == "" {
		return nil
	}
	lock := toollock.Read(m.LockPath).Tools
	out := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if cur, ok := lock[toollock.Key(serverName, tool.Name)]; ok && cur.Hash != toollock.Hash(tool) {
			out[tool.Name] = true
		}
	}
	return out
}

// commandList — one catalog entry per tool, with the quarantine mark.
func (m *Manager) commandList(serverName string, tools []*mcp.Tool) []commandInfo {
	changed := m.changedTools(serverName, tools)
	out := make([]commandInfo, 0, len(tools))
	for _, tool := range tools {
		out = append(out, commandInfo{
			Server:      serverName,
			Name:        tool.Name,
			Command:     tool.Name,
			Description: tool.Description,
			Changed:     changed[tool.Name],
		})
	}
	return out
}

// quarantineResult keeps the invoke_command shape: the caller sees isError and
// a text naming both hashes and the command that lifts the stop.
func quarantineResult(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
		Meta:    mcp.Meta{"lazyMcpPolicy": "quarantine"},
	}
}

func quarantineMessage(server, tool, approved, now string) string {
	return toollock.Message(server, tool, approved, now, os.Getenv("MCPRIG_SET"))
}

// lockFailureMessage — отказ при неведомом состоянии карантина: он называет
// причину, чтобы человек мог починить файл, а не искать её в логе прокси.
func lockFailureMessage(server, tool, what string, err error) string {
	return fmt.Sprintf("🚫 Карантин неизвестен: %s (%v) — вызов %s/%s отклонён", what, err, server, tool)
}
