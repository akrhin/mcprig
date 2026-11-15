package mcpserver

// Карантин дескрипторов: первое появление инструмента одобряется само, смена
// дескриптора у одобренного — стоп до одобрения человеком. Смена версии здесь
// — перезапуск набора: новый процесс видит новый дескриптор, а файл карантина
// переживает перезапуск.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/toollock"
)

// descriptorUpstream — in-memory апстрим с одним инструментом echo, описание
// которого и есть версия дескриптора; ping не меняется и служит соседом.
func descriptorUpstream(ctx context.Context, version string) (*mcp.ClientSession, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: "downstream", Version: "test"}, nil)
	for _, name := range []string{"echo", "ping"} {
		description := ""
		if name == "echo" {
			description = version
		}
		s.AddTool(&mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: objectSchema(map[string]any{"text": map[string]any{"type": "string"}}),
		}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: name + ":" + string(req.Params.Arguments),
			}}}, nil
		})
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		return nil, err
	}
	return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(context.Background(), ct, nil)
}

// descriptorManager — менеджер одного сервера alpha с заданной версией
// дескриптора и общим файлом карантина.
func descriptorManager(t *testing.T, version, lockPath string) *Manager {
	t.Helper()
	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	m.LockPath = lockPath
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		return descriptorUpstream(ctx, version)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tools.lock.json")
}

// catalog заполняет кэш инструментов и возвращает разобранный ответ. Один
// сервер — ответ плоским списком, как его отдаёт list_commands.
func catalog(t *testing.T, cs *mcp.ClientSession) []commandInfo {
	t.Helper()
	var out []commandInfo
	if err := json.Unmarshal([]byte(resultText(t, call(t, cs, "list_commands", map[string]any{"server": "alpha"}))), &out); err != nil {
		t.Fatalf("list_commands: %v", err)
	}
	return out
}

func invoke(t *testing.T, cs *mcp.ClientSession, tool string) *mcp.CallToolResult {
	t.Helper()
	return call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": tool, "parameters": map[string]any{"text": "hi"},
	})
}

// Первое появление инструмента — первое доверие: вызов идёт сразу, а в файле
// остаётся запись auto, чтобы следующее изменение уже требовало человека.
func TestFirstSightIsAutoApproved(t *testing.T) {
	lock := lockPath(t)
	m := descriptorManager(t, "v1", lock)
	cs := clientFor(t, m.Server())
	catalog(t, cs)

	if res := invoke(t, cs, "echo"); res.IsError {
		t.Fatalf("новый инструмент отбит: %s", resultText(t, res))
	}
	entry, ok := toollock.Read(lock).Tools["alpha/echo"]
	if !ok || entry.Hash == "" || entry.ApprovedBy != "auto" {
		t.Fatalf("нет автоодобрения: %+v", entry)
	}
}

// Одобрили v1, каталог вернул v2 — вызов отбит до одобрения, и текст называет
// команду, которая снимает стоп.
func TestChangedDescriptorBlocksInvoke(t *testing.T) {
	t.Setenv("MCPRIG_SET", "main")
	lock := lockPath(t)
	first := descriptorManager(t, "v1", lock)
	cs := clientFor(t, first.Server())
	catalog(t, cs)
	if res := invoke(t, cs, "echo"); res.IsError {
		t.Fatalf("v1 отбит: %s", resultText(t, res))
	}

	second := descriptorManager(t, "v2", lock)
	cs2 := clientFor(t, second.Server())
	catalog(t, cs2)
	res := invoke(t, cs2, "echo")
	if !res.IsError {
		t.Fatalf("изменившийся инструмент выполнен: %s", resultText(t, res))
	}
	text := resultText(t, res)
	for _, want := range []string{"🚫 Инструмент изменился: alpha/echo", "mcprig approve --set main alpha/echo"} {
		if !strings.Contains(text, want) {
			t.Fatalf("в отказе нет %q: %s", want, text)
		}
	}
	// Сосед не тронут: карантин точечный, а не на сервер целиком.
	if res := invoke(t, cs2, "ping"); res.IsError {
		t.Fatalf("неизменившийся сосед отбит: %s", resultText(t, res))
	}
}

// Одобрение снимает стоп: панель пишет тот же файл, что читает прокси.
func TestApproveAllowsChangedTool(t *testing.T) {
	lock := lockPath(t)
	first := descriptorManager(t, "v1", lock)
	catalog(t, clientFor(t, first.Server()))

	second := descriptorManager(t, "v2", lock)
	cs := clientFor(t, second.Server())
	catalog(t, cs)
	if !invoke(t, cs, "echo").IsError {
		t.Fatal("каталог v2 не попал под карантин")
	}

	state := toollock.Read(lock)
	entry, ok := toollock.Approve(state.Tools["alpha/echo"], "panel", "2026-11-15T00:00:00Z")
	if !ok {
		t.Fatalf("одобрять нечего: %+v", state.Tools["alpha/echo"])
	}
	state.Tools["alpha/echo"] = entry
	if err := toollock.Write(lock, state); err != nil {
		t.Fatalf("запись одобрения: %v", err)
	}

	if res := invoke(t, cs, "echo"); res.IsError {
		t.Fatalf("после одобрения вызов отбит: %s", resultText(t, res))
	}
	if got := toollock.Read(lock).Tools["alpha/echo"]; got.Seen != "" || got.ApprovedBy != "panel" {
		t.Fatalf("одобрение не сохранилось: %+v", got)
	}
}

// Карантин живёт на диске: третий запуск видит его без новых одобрений.
func TestLockFilePersistsAcrossRestart(t *testing.T) {
	lock := lockPath(t)
	catalog(t, clientFor(t, descriptorManager(t, "v1", lock).Server()))

	for i := 0; i < 2; i++ {
		cs := clientFor(t, descriptorManager(t, "v2", lock).Server())
		catalog(t, cs)
		if res := invoke(t, cs, "echo"); !res.IsError {
			t.Fatalf("запуск %d: карантин потерян", i+2)
		}
	}
}

// Один изменившийся инструмент не тянет за собой соседей по пачке.
func TestInvokeManyBlocksQuarantined(t *testing.T) {
	lock := lockPath(t)
	catalog(t, clientFor(t, descriptorManager(t, "v1", lock).Server()))

	cs := clientFor(t, descriptorManager(t, "v2", lock).Server())
	catalog(t, cs)
	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		echoCall("alpha", "bad", 0),
		map[string]any{"server": "alpha", "command_name": "ping", "parameters": map[string]any{"text": "ok"}},
	}})
	if res.IsError {
		t.Fatalf("веер упал целиком: %s", resultText(t, res))
	}
	out := manyAnswers(t, res)
	if out[0].OK || !strings.Contains(out[0].Error, "изменился") {
		t.Fatalf("карантин не отбил вызов: %+v", out[0])
	}
	if !out[1].OK {
		t.Fatalf("сосед отбит: %+v", out[1])
	}
}

// list_commands показывает изменившийся инструмент, а не прячет его: клиент
// видит, что инструмент есть и почему он ждёт одобрения.
func TestListShowsChanged(t *testing.T) {
	lock := lockPath(t)
	catalog(t, clientFor(t, descriptorManager(t, "v1", lock).Server()))

	cmds := catalog(t, clientFor(t, descriptorManager(t, "v2", lock).Server()))
	byName := map[string]commandInfo{}
	for _, cmd := range cmds {
		byName[cmd.Name] = cmd
	}
	if !byName["echo"].Changed {
		t.Fatalf("echo без пометки: %+v", byName["echo"])
	}
	if byName["ping"].Changed {
		t.Fatalf("ping помечен зря: %+v", byName["ping"])
	}
}

// twoServerManager — alpha и beta в одном менеджере: refresh одного не должен
// трогать кэш другого.
func twoServerManager(t *testing.T, version, lock string) *Manager {
	t.Helper()
	m := New(&config.Config{Servers: []*config.Server{stdio("alpha"), stdio("beta")}}, "test", nil)
	m.LockPath = lock
	m.dial = func(ctx context.Context, _ *config.Server) (*mcp.ClientSession, error) {
		return descriptorUpstream(ctx, version)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// refresh одного сервера чистит только его кэш. Раньше он чистил все, и вызов
// соседа шёл мимо карантина: сверять дескриптор было не с чем.
func TestRefreshLeavesOtherServerCache(t *testing.T) {
	lock := lockPath(t)
	catalogBoth := func(m *Manager) {
		t.Helper()
		for _, name := range []string{"alpha", "beta"} {
			if _, err := m.commandsOf(context.Background(), name); err != nil {
				t.Fatalf("каталог %s: %v", name, err)
			}
		}
	}
	catalogBoth(twoServerManager(t, "v1", lock))

	m := twoServerManager(t, "v2", lock)
	cs := clientFor(t, m.Server())
	catalogBoth(m)
	call(t, cs, "list_commands", map[string]any{"server": "alpha", "refresh": true})

	if m.find("beta").cachedTools() == nil {
		t.Fatal("refresh alpha опустошил кэш beta — карантин проверять не по чему")
	}
	// И собственный сервер после refresh по-прежнему под карантином.
	if res := call(t, cs, "invoke_command", map[string]any{
		"server": "alpha", "command_name": "echo", "parameters": map[string]any{"text": "hi"},
	}); !res.IsError {
		t.Fatalf("после refresh изменившийся инструмент выполнен: %s", resultText(t, res))
	}
}

// Пустой кэш каталога не открывает обход: первый же вызов после старта набора
// обязан сверить дескриптор, иначе карантин обходится перезапуском.
func TestQuarantineHoldsWithoutCatalog(t *testing.T) {
	lock := lockPath(t)
	catalog(t, clientFor(t, descriptorManager(t, "v1", lock).Server()))

	// Каталог не запрашивается: кэш пуст, как сразу после старта набора.
	cs := clientFor(t, descriptorManager(t, "v2", lock).Server())
	res := invoke(t, cs, "echo")
	if !res.IsError {
		t.Fatalf("карантин обойдён пустым кэшем: %s", resultText(t, res))
	}
	if text := resultText(t, res); !strings.Contains(text, "изменился: alpha/echo") {
		t.Fatalf("отказ без пометки о смене: %s", text)
	}
}

// Каталог не прочитан (апстрим лежит) — вызов не идёт: сверить дескриптор
// нечем, и «наверное, всё в порядке» здесь недопустимо.
func TestQuarantineFailsClosedWhenCatalogUnavailable(t *testing.T) {
	m := New(&config.Config{Servers: []*config.Server{stdio("alpha")}}, "test", nil)
	m.LockPath = lockPath(t)
	m.dial = func(context.Context, *config.Server) (*mcp.ClientSession, error) {
		return nil, errors.New("апстрим недоступен")
	}
	t.Cleanup(func() { _ = m.Close() })

	res := invoke(t, clientFor(t, m.Server()), "echo")
	if !res.IsError {
		t.Fatalf("вызов пошёл без каталога: %s", resultText(t, res))
	}
	if text := resultText(t, res); !strings.Contains(text, "карантин: каталог") {
		t.Fatalf("отказ не назвал карантин: %s", text)
	}
}

// v1 → v2 → v1: пометка о смене снимается, когда дескриптор вернулся к
// одобренному. Иначе --all одобрил бы хэш v2 и запер рабочий v1.
func TestSeenClearsWhenDescriptorReverts(t *testing.T) {
	lock := lockPath(t)
	catalog(t, clientFor(t, descriptorManager(t, "v1", lock).Server()))
	catalog(t, clientFor(t, descriptorManager(t, "v2", lock).Server()))
	if entry := toollock.Read(lock).Tools["alpha/echo"]; entry.Seen == "" {
		t.Fatalf("смена v2 не замечена: %+v", entry)
	}

	cs := clientFor(t, descriptorManager(t, "v1", lock).Server())
	catalog(t, cs)
	entry := toollock.Read(lock).Tools["alpha/echo"]
	if entry.Seen != "" {
		t.Fatalf("пометка о смене осталась после отката: %+v", entry)
	}
	if _, ok := toollock.Approve(entry, "panel", "2026-11-15T00:00:00Z"); ok {
		t.Fatal("одобрять нечего: дескриптор совпадает с одобренным")
	}
	if res := invoke(t, cs, "echo"); res.IsError {
		t.Fatalf("рабочий v1 заперт: %s", resultText(t, res))
	}
}

// Файл одобрений не читается (на его месте каталог) — вызов не идёт: состояние
// карантина неизвестно, и тихо разрешать его нельзя.
func TestQuarantineFailsClosedOnUnreadableLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "tools.lock.json")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatalf("каталог вместо файла: %v", err)
	}
	cs := clientFor(t, descriptorManager(t, "v1", lock).Server())
	catalog(t, cs)

	res := invoke(t, cs, "echo")
	if !res.IsError {
		t.Fatalf("вызов прошёл при нечитаемом файле: %s", resultText(t, res))
	}
	if text := resultText(t, res); !strings.Contains(text, "не прочитан") {
		t.Fatalf("отказ не назвал причину: %s", text)
	}
}

// Запись файла не удалась — вызов не идёт: одобрение нового инструмента в файл
// не попало, и решать по устаревшему карантину нельзя.
func TestQuarantineFailsClosedOnWriteError(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "tools.lock.json")
	if err := toollock.Write(lock, toollock.File{Tools: map[string]toollock.Entry{}}); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("запрет записи: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	cs := clientFor(t, descriptorManager(t, "v1", lock).Server())
	catalog(t, cs) // observe не открыл <lock>.lock: запись не состоялась

	res := invoke(t, cs, "echo")
	if !res.IsError {
		t.Fatalf("вызов прошёл при неудавшейся записи: %s", resultText(t, res))
	}
	if text := resultText(t, res); !strings.Contains(text, "не записан") {
		t.Fatalf("отказ не назвал причину: %s", text)
	}
}
