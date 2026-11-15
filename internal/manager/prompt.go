package manager

// Промт для агента: вводная, каталог инструментов набора и задача — одним
// текстом, который агент читает и копирует сам. Собирается здесь, а не в
// браузере: каталог со схемами и маска секретов живут на панели.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/registry"
	"github.com/akrhin/mcprig/internal/toollock"
)

const (
	// promptSchemaLimit — сколько места отдано схеме одного инструмента:
	// дальше она перестаёт помогать и только съедает промт.
	promptSchemaLimit = 4000
	// promptSizeLimit — потолок всего промта: это текст для человека и
	// агента, а не файл на диске.
	promptSizeLimit = 64 << 10

	promptToolsHeading = "## Инструменты"
	promptTaskHeading  = "## Задача"
)

type promptTool struct {
	Name        string
	Description string
	Schema      string
}

type promptServer struct {
	Name  string
	Tools []promptTool
}

// cmdInfo — ответ list_commands по одному инструменту.
type cmdInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// promptCatalog — инструменты по серверам в порядке набора плюс заметки о
// том, что не удалось получить.
type promptCatalog struct {
	Servers []promptServer
	Notes   []string
}

func emptyCatalog(names []string) promptCatalog {
	out := promptCatalog{Servers: make([]promptServer, len(names))}
	for i, name := range names {
		out.Servers[i].Name = name
	}
	return out
}

// apiSetPrompt собирает промт: {task, intro, servers} → {prompt, notes}.
func (m *Manager) apiSetPrompt(req *request) error {
	id := req.args[0]
	set := m.store.Set(id)
	if set == nil {
		return notFound("набор " + id + " не найден")
	}
	var in struct {
		Task    string   `json:"task"`
		Intro   string   `json:"intro"`
		Servers []string `json:"servers"`
	}
	if err := decodeJSON(req.r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Task) == "" {
		return badRequest("задача не задана")
	}
	names, err := promptServers(set, in.Servers)
	if err != nil {
		return err
	}

	cat := emptyCatalog(names)
	running := m.running(set)
	if running {
		cat = m.catalog(req.r.Context(), set, names)
	}
	// Инструменты, которых живой каталог не дал (набор остановлен или не
	// ответил), берём из tools.lock.json: имена есть, схем нет.
	fromLock := fillFromLock(&cat, m.rt.dataDir, id)
	if !running {
		cat.Notes = append(cat.Notes, "набор не запущен, схемы недоступны")
	} else if fromLock && len(cat.Notes) > 0 {
		cat.Notes = append(cat.Notes, "часть инструментов взята из tools.lock.json: схемы недоступны")
	}

	secrets := m.promptSecretValues(set)
	if set.Token != "" {
		secrets = append(secrets, set.Token)
	}
	for i := range cat.Notes {
		cat.Notes[i] = config.ScrubSecrets(cat.Notes[i], secrets)
	}

	prompt, notes := buildPrompt(set.ID, in.Intro, in.Task, cat)
	notes = append(cat.Notes, notes...)
	if notes == nil {
		notes = []string{}
	}
	writeJSON(req.w, http.StatusOK, map[string]any{"prompt": prompt, "notes": notes})
	return nil
}

// promptServers — серверы промта в порядке набора. Фильтр сужает список, но
// чужое имя не пропускает: иначе в промт попал бы сервер другого набора.
func promptServers(set *registry.Set, want []string) ([]string, error) {
	if len(want) == 0 {
		return set.Servers, nil
	}
	known := make(map[string]bool, len(set.Servers))
	for _, name := range set.Servers {
		known[name] = true
	}
	out := make([]string, 0, len(want))
	seen := make(map[string]bool, len(want))
	for _, name := range want {
		name = strings.TrimSpace(name)
		if !known[name] {
			return nil, badRequest("сервер " + name + " не входит в набор " + set.ID)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// promptSecretValues — значения переменных, которые видят серверы набора:
// текст ошибки пробы уходит в промт только через маску, как в list_servers.
func (m *Manager) promptSecretValues(set *registry.Set) []string {
	if m.sec == nil {
		return nil
	}
	servers := m.store.Servers()
	byName := make(map[string]*config.Server, len(servers))
	for _, srv := range servers {
		byName[srv.Name] = srv
	}
	seen := map[string]bool{}
	names := make([]string, 0, 8)
	for _, name := range set.Servers {
		srv, ok := byName[name]
		if !ok || srv == nil {
			continue
		}
		for _, ref := range serverEnvRefs(srv) {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			names = append(names, ref)
		}
	}
	values := make([]string, 0, len(names))
	for _, v := range m.sec.Resolve(names) {
		if v.value != "" {
			values = append(values, v.value)
		}
	}
	return values
}

// fillFromLock добавляет имена из tools.lock.json серверам без живого
// каталога; true — что-то добавилось.
func fillFromLock(cat *promptCatalog, dataDir, id string) bool {
	lock := toollock.Read(toollock.Path(dataDir, id))
	if len(lock.Tools) == 0 {
		return false
	}
	byServer := make(map[string][]string, len(cat.Servers))
	for key := range lock.Tools {
		srv, tool := toollock.Split(key)
		byServer[srv] = append(byServer[srv], tool)
	}
	added := false
	for i := range cat.Servers {
		if len(cat.Servers[i].Tools) > 0 {
			continue
		}
		tools := byServer[cat.Servers[i].Name]
		sort.Strings(tools)
		for _, name := range tools {
			cat.Servers[i].Tools = append(cat.Servers[i].Tools, promptTool{Name: name})
			added = true
		}
	}
	return added
}

// buildPrompt склеивает три части. Обрезка идёт по блокам перечня, поэтому
// вводная и задача остаются целыми; заметки говорят, что именно потерялось.
func buildPrompt(id, intro, task string, cat promptCatalog) (string, []string) {
	if strings.TrimSpace(intro) == "" {
		intro = "Ты работаешь с MCP-набором " + id + " (mcprig). Инструменты ниже — " +
			"MCP-инструменты этого набора: вызывай их как обычно и не выдумывай тех, которых нет в списке."
	}
	var notes []string
	blocks := make([]string, 0, 16)
	for _, srv := range cat.Servers {
		var b strings.Builder
		b.WriteString("\n### " + srv.Name + "\n")
		if len(srv.Tools) == 0 {
			b.WriteString("\n(инструментов нет)\n")
		}
		for _, tool := range srv.Tools {
			b.WriteString("\n- `" + tool.Name + "`")
			if tool.Description != "" {
				b.WriteString(" — " + strings.Join(strings.Fields(tool.Description), " "))
			}
			b.WriteString("\n")
			if tool.Schema == "" {
				continue
			}
			schema := tool.Schema
			if len(schema) > promptSchemaLimit {
				schema = ellipsis(schema, promptSchemaLimit)
				notes = append(notes, "схема "+srv.Name+"/"+tool.Name+" обрезана до "+
					strconv.Itoa(promptSchemaLimit)+" символов")
			}
			b.WriteString("\n  ```json\n  " + schema + "\n  ```\n")
		}
		blocks = append(blocks, b.String())
	}
	if len(cat.Servers) == 0 {
		blocks = append(blocks, "\n(серверов нет)\n")
	}

	head := intro + "\n\n" + promptToolsHeading + "\n"
	tail := "\n" + promptTaskHeading + "\n\n" + task + "\n"
	budget := promptSizeLimit - len(head) - len(tail)
	var body strings.Builder
	for i, block := range blocks {
		if body.Len()+len(block) > budget {
			notes = append(notes, "перечень инструментов обрезан: осталось "+
				strconv.Itoa(len(blocks)-i)+" блок(ов), потолок промта "+
				strconv.Itoa(promptSizeLimit)+" байт")
			break
		}
		body.WriteString(block)
	}
	prompt := head + body.String() + tail
	if len(prompt) > promptSizeLimit {
		prompt = ellipsis(prompt, promptSizeLimit)
		notes = append(notes, "промт обрезан до "+strconv.Itoa(promptSizeLimit)+" байт")
	}
	return prompt, notes
}

// ellipsis — срез не длиннее n байт вместе с многоточием и по границе руны:
// битый UTF-8 в промте агент прочитает мусором.
func ellipsis(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// fetchSetCatalog читает живой каталог набора тем же путём, что проба
// здоровья: клиент MCP с токеном набора. Имена и описания — list_commands,
// схемы — describe_commands.
func fetchSetCatalog(ctx context.Context, host string, port int, token string, open bool, names []string, version string) promptCatalog {
	out := emptyCatalog(names)
	if len(names) == 0 {
		return out
	}
	if token == "" && !open {
		out.Notes = append(out.Notes, "у набора нет активного ключа — каталог недоступен")
		return out
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcprig", Version: version}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/mcp",
		HTTPClient:           &http.Client{Transport: &bearerTransport{token: token}, Timeout: healthLoadTimeout},
		DisableStandaloneSSE: true,
	}
	ctx, cancel := context.WithTimeout(ctx, healthLoadTimeout)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		out.Notes = append(out.Notes, "каталог набора недоступен: "+err.Error())
		return out
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_commands",
		Arguments: map[string]any{"servers": names},
	})
	if err != nil {
		out.Notes = append(out.Notes, "list_commands: "+err.Error())
		return out
	}
	listed := map[string]json.RawMessage{}
	if text := firstText(res); text != "" {
		if err := json.Unmarshal([]byte(text), &listed); err != nil {
			out.Notes = append(out.Notes, "list_commands: ответ не разобран: "+err.Error())
			return out
		}
	}
	for i := range out.Servers {
		name := out.Servers[i].Name
		raw, ok := listed[name]
		if !ok {
			continue
		}
		var cmds []cmdInfo
		if err := json.Unmarshal(raw, &cmds); err != nil {
			// Сервер не ответил — набор отдаёт {"error": "…"} вместо списка.
			var failed struct {
				Error string `json:"error"`
			}
			msg := "список не разобран"
			if json.Unmarshal(raw, &failed) == nil && failed.Error != "" {
				msg = failed.Error
			}
			out.Notes = append(out.Notes, name+": "+msg)
			continue
		}
		sort.Slice(cmds, func(a, b int) bool { return cmds[a].Name < cmds[b].Name })
		for _, cmd := range cmds {
			out.Servers[i].Tools = append(out.Servers[i].Tools, promptTool{Name: cmd.Name, Description: cmd.Description})
		}
		if len(cmds) > 0 {
			schemas := fetchSchemas(ctx, session, name, cmds)
			for j := range out.Servers[i].Tools {
				out.Servers[i].Tools[j].Schema = schemas[out.Servers[i].Tools[j].Name]
			}
		}
	}
	return out
}

// fetchSchemas — describe_commands одного сервера, схемы в компактном JSON.
func fetchSchemas(ctx context.Context, session *mcp.ClientSession, server string, cmds []cmdInfo) map[string]string {
	names := make([]string, len(cmds))
	for i, cmd := range cmds {
		names[i] = cmd.Name
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "describe_commands",
		Arguments: map[string]any{"server": server, "command_names": names},
	})
	if err != nil {
		return nil
	}
	var payload struct {
		Commands []struct {
			Name        string `json:"name"`
			InputSchema any    `json:"inputSchema"`
		} `json:"commands"`
	}
	if text := firstText(res); text != "" {
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			return nil
		}
	}
	out := make(map[string]string, len(payload.Commands))
	for _, cmd := range payload.Commands {
		if cmd.InputSchema == nil {
			continue
		}
		if compact, err := json.Marshal(cmd.InputSchema); err == nil {
			out[cmd.Name] = string(compact)
		}
	}
	return out
}
