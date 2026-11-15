package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
)

const readerFP = "aaaaaa"

// profileManager — набор из alpha и beta, ключ aaaaaa с профилем reader.
// Инструменты апстрима (см. upstream): echo, danger-zone, boom.
func profileManager(t *testing.T, profiles map[string]*config.Profile, perms *config.Permissions) *Manager {
	t.Helper()
	var dials int32
	m := testManager(&config.Config{
		Servers:     []*config.Server{stdio("alpha"), stdio("beta")},
		Permissions: perms,
		Profiles:    profiles,
		Transport:   &config.HTTPTransport{KeyProfiles: map[string]string{readerFP: "reader"}},
	}, &dials, 0)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func keyCtx() context.Context { return stats.WithClientKey(context.Background(), readerFP) }

func toolReq(name, body string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: []byte(body)}}
}

// listNames разбирает ответ list_commands в имена отданных инструментов.
func listNames(t *testing.T, res *mcp.CallToolResult) []string {
	t.Helper()
	if res.IsError {
		t.Fatalf("list_commands вернул ошибку: %s", resultText(t, res))
	}
	var cmds []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &cmds); err != nil {
		t.Fatalf("разбор каталога: %v", err)
	}
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	return names
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// Профиль сужает каталог: запрещённых инструментов в нём нет ни в одном
// сервере. Ключ без профиля видит всё, deny ["*"] даёт пустой каталог.
func TestProfileFiltersListCommands(t *testing.T) {
	m := profileManager(t, map[string]*config.Profile{
		"reader": {Deny: []string{"danger-*"}},
	}, nil)

	res, err := m.listCommands(keyCtx(), toolReq("list_commands", `{"server":"alpha"}`))
	if err != nil {
		t.Fatalf("listCommands: %v", err)
	}
	names := listNames(t, res)
	if hasName(names, "danger-zone") {
		t.Fatalf("профиль отдал запрещённый инструмент: %v", names)
	}
	if !hasName(names, "echo") || !hasName(names, "boom") {
		t.Fatalf("профиль выкинул разрешённые: %v", names)
	}

	// Мультисерверный запрос фильтруется так же.
	res, err = m.listCommands(keyCtx(), toolReq("list_commands", `{"servers":["alpha","beta"]}`))
	if err != nil {
		t.Fatalf("listCommands: %v", err)
	}
	var byServer map[string][]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &byServer); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	for server, cmds := range byServer {
		for _, c := range cmds {
			if c.Name == "danger-zone" {
				t.Fatalf("сервер %s отдал запрещённый инструмент", server)
			}
		}
	}

	// Ключ без профиля видит всё — поведение не изменилось.
	res, err = m.listCommands(context.Background(), toolReq("list_commands", `{"server":"alpha"}`))
	if err != nil {
		t.Fatalf("listCommands: %v", err)
	}
	if !hasName(listNames(t, res), "danger-zone") {
		t.Fatal("ключ без профиля не видит danger-zone")
	}
}

// deny ["*"] прячет каталог целиком; имя профиля, которого нет в наборе,
// тоже запрещает всё — удаление профиля не расширяет ключ.
func TestProfileDenyAllEmptiesCatalog(t *testing.T) {
	m := profileManager(t, map[string]*config.Profile{"reader": {Deny: []string{"*"}}}, nil)
	res, err := m.listCommands(keyCtx(), toolReq("list_commands", `{"server":"alpha"}`))
	if err != nil {
		t.Fatalf("listCommands: %v", err)
	}
	if names := listNames(t, res); len(names) != 0 {
		t.Fatalf("deny [*] отдал %v", names)
	}

	// Профиль reader в наборе отсутствует: ключ получает отказ, а не всё.
	m = profileManager(t, nil, nil)
	res, err = m.listCommands(keyCtx(), toolReq("list_commands", `{"server":"alpha"}`))
	if err != nil {
		t.Fatalf("listCommands: %v", err)
	}
	if names := listNames(t, res); len(names) != 0 {
		t.Fatalf("неизвестный профиль отдал %v", names)
	}
}

// permissions проверяются первыми: профиль не может разрешить запрещённое,
// и отказ говорит о правилах набора, а не о профиле.
func TestProfileCannotWidenPermissions(t *testing.T) {
	perms := &config.Permissions{Rules: []config.Rule{
		{Server: "alpha", Pattern: "danger-*", Action: "deny", Message: "запрещено набором"},
	}}
	m := profileManager(t, map[string]*config.Profile{
		"reader": {Allow: []string{"*"}},
	}, perms)

	res, err := m.invokeCommand(keyCtx(), toolReq("invoke_command", `{"server":"alpha","command":"danger-zone"}`))
	if err != nil {
		t.Fatalf("invokeCommand: %v", err)
	}
	text := resultText(t, res)
	if !res.IsError || !strings.Contains(text, "запрещено набором") {
		t.Fatalf("ждали отказ permissions, получили %q", text)
	}
	if strings.Contains(text, "профиль reader") {
		t.Fatalf("профиль перебил permissions: %q", text)
	}
}

// invoke_command и invoke_many отвергают то, что профиль не разрешает,
// ошибкой инструмента с именем профиля и адресом вызова.
func TestProfileDeniesInvoke(t *testing.T) {
	m := profileManager(t, map[string]*config.Profile{
		"reader": {Deny: []string{"danger-*"}},
	}, nil)

	res, err := m.invokeCommand(keyCtx(), toolReq("invoke_command", `{"server":"alpha","command":"danger-zone"}`))
	if err != nil {
		t.Fatalf("invokeCommand: %v", err)
	}
	if text := resultText(t, res); !res.IsError || !strings.Contains(text, "профиль reader не разрешает alpha/danger-zone") {
		t.Fatalf("invoke_command: %q", text)
	}

	// Разрешённый вызов проходит — отказ не глушит ключ целиком.
	res, err = m.invokeCommand(keyCtx(), toolReq("invoke_command", `{"server":"alpha","command":"echo"}`))
	if err != nil || res.IsError {
		t.Fatalf("echo отклонён: %v %s", err, resultText(t, res))
	}

	res, err = m.invokeMany(keyCtx(), toolReq("invoke_many", `{"calls":[
		{"server":"alpha","command":"danger-zone"},
		{"server":"alpha","command":"echo"}]}`))
	if err != nil {
		t.Fatalf("invokeMany: %v", err)
	}
	answers := manyAnswers(t, res)
	if len(answers) != 2 {
		t.Fatalf("ответов %d", len(answers))
	}
	if answers[0].Error == "" || !strings.Contains(answers[0].Error, "профиль reader не разрешает") {
		t.Fatalf("запрещённый вызов не отвергнут: %+v", answers[0])
	}
	if answers[1].Error != "" || answers[1].Result == nil {
		t.Fatalf("разрешённый вызов не прошёл: %+v", answers[1])
	}
}

// describe_commands подчиняется профилю так же, как list_commands: схема
// скрытого инструмента не отдаётся, разрешённые остаются, ключ без профиля
// получает всё.
func TestProfileFiltersDescribeCommands(t *testing.T) {
	m := profileManager(t, map[string]*config.Profile{
		"reader": {Deny: []string{"danger-*"}},
	}, nil)

	res, err := m.describeCommands(keyCtx(), toolReq("describe_commands",
		`{"server":"alpha","command_names":["danger-zone","echo"]}`))
	if err != nil {
		t.Fatalf("describeCommands: %v", err)
	}
	if res.IsError {
		t.Fatalf("describe_commands вернул ошибку: %s", resultText(t, res))
	}
	var out describeOut
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("разбор схем: %v", err)
	}
	if len(out.Commands) != 1 || out.Commands[0].Name != "echo" {
		t.Fatalf("профиль отдал не те схемы: %+v", out.Commands)
	}
	if len(out.Missing) != 1 || out.Missing[0] != "danger-zone" {
		t.Fatalf("запрещённая схема не спрятана: %+v", out.Missing)
	}

	res, err = m.describeCommands(context.Background(), toolReq("describe_commands",
		`{"server":"alpha","command_names":["danger-zone","echo"]}`))
	if err != nil {
		t.Fatalf("describeCommands без профиля: %v", err)
	}
	out = describeOut{}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("разбор схем: %v", err)
	}
	if len(out.Commands) != 2 {
		t.Fatalf("ключ без профиля потерял схемы: %+v", out)
	}
}

// Профиль подписанного ключа работает: ключ ищется по отпечатку, подпись
// (clientNames) на него не влияет.
func TestProfileWorksForSignedKey(t *testing.T) {
	var dials int32
	m := testManager(&config.Config{
		Servers:   []*config.Server{stdio("alpha")},
		Profiles:  map[string]*config.Profile{"reader": {Deny: []string{"danger-*"}}},
		Transport: &config.HTTPTransport{KeyProfiles: map[string]string{readerFP: "reader"}},
	}, &dials, 0)
	t.Cleanup(func() { _ = m.Close() })
	ctx := stats.WithClientKey(stats.WithClient(context.Background(), "hermes"), readerFP)

	res, err := m.invokeCommand(ctx, toolReq("invoke_command", `{"server":"alpha","command":"danger-zone"}`))
	if err != nil {
		t.Fatalf("invokeCommand: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "профиль reader не разрешает") {
		t.Fatalf("подписанный ключ прошёл мимо профиля: %q", resultText(t, res))
	}
}
