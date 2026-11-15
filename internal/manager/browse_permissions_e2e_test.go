package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akrhin/mcprig/internal/toollock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type browseFixtureIn struct {
	Text string `json:"text,omitempty"`
}

// browseUpstream — stdio-сервер-фикстура. Каждый вызов инструмента дописывает
// строку в файл из BROWSE_CALL_LOG: тест видит, дошёл ли вызов до апстрима.
func browseUpstream() {
	logPath := os.Getenv("BROWSE_CALL_LOG")
	record := func(name string) {
		if logPath == "" {
			return
		}
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		_, _ = f.WriteString(name + "\n")
		_ = f.Close()
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "browse-fixture", Version: "0"}, nil)
	// BROWSE_TOOL_VERSION меняет дескриптор: так тест карантина изображает
	// обновление сервера, не пересобирая фикстуру.
	desc := "Allowed by permission rules."
	if v := os.Getenv("BROWSE_TOOL_VERSION"); v != "" {
		desc += " " + v
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "read_all", Description: desc},
		func(context.Context, *mcp.CallToolRequest, browseFixtureIn) (*mcp.CallToolResult, any, error) {
			record("read_all")
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "read_all ok"}}}, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "danger_rm", Description: "Denied by permission rules."},
		func(context.Context, *mcp.CallToolRequest, browseFixtureIn) (*mcp.CallToolResult, any, error) {
			record("danger_rm")
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "danger_rm ok"}}}, nil, nil
		})
	for _, r := range []struct{ uri, text string }{
		{"test://open", "open resource"},
		{"test://secret", "secret resource"},
	} {
		srv.AddResource(&mcp.Resource{URI: r.uri, Name: r.uri},
			func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				record(r.uri)
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: r.uri, Text: r.text}}}, nil
			})
	}
	for _, name := range []string{"open_prompt", "secret_prompt"} {
		srv.AddPrompt(&mcp.Prompt{Name: name},
			func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				record(name)
				return &mcp.GetPromptResult{Description: name + " ok"}, nil
			})
	}
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// Цепочка данных целиком: сервер создаётся тем же HTTP-маршрутом, что и из
// панели, browse открывается по имени из реестра, гейт берёт правила из
// srv.Permissions, а запрещённый вызов не доходит до апстрима.
func TestBrowsePermissionsEndToEnd(t *testing.T) {
	if os.Getenv("BROWSE_UPSTREAM") == "1" {
		browseUpstream()
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(t.TempDir(), "calls.log")
	e := newEnv(t, AuthOptions{Off: true})

	w := e.do(t, http.MethodPost, "/api/servers", map[string]any{
		"name":    "alpha",
		"command": []string{exe, "-test.run=TestBrowsePermissionsEndToEnd$"},
		"env":     map[string]string{"BROWSE_UPSTREAM": "1", "BROWSE_CALL_LOG": callLog},
		"permissions": map[string]any{
			"default": "deny",
			"rules": []map[string]string{
				{"pattern": "read_*", "action": "allow"},
				{"pattern": "danger_*", "action": "deny", "message": "только чтение"},
				{"pattern": "resource:test://open", "action": "allow"},
				{"pattern": "resource:test://secret", "action": "deny", "message": "секрет закрыт"},
				{"pattern": "prompt:open_*", "action": "allow"},
				{"pattern": "prompt:secret_*", "action": "deny", "message": "промпт закрыт"},
			},
		},
	})
	status(t, w, http.StatusCreated)

	w = e.do(t, http.MethodPost, "/api/browse", map[string]any{"server": "alpha"})
	status(t, w, http.StatusOK)
	var view browseView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("ответ browse: %v (%s)", err, w.Body.String())
	}
	if view.ID == "" {
		t.Fatalf("browse без id: %s", w.Body.String())
	}

	// Запрещённый: 403 с текстом правила и ни одного вызова апстрима.
	w = e.do(t, http.MethodPost, "/api/browse/"+view.ID+"/tool", map[string]any{"name": "danger_rm"})
	status(t, w, http.StatusForbidden)
	if body := w.Body.String(); !strings.Contains(body, "только чтение") {
		t.Fatalf("в 403 нет текста правила: %s", body)
	}
	if calls := readCalls(t, callLog); len(calls) != 0 {
		t.Fatalf("запрещённый вызов дошёл до апстрима: %v", calls)
	}

	// Разрешённый правилом allow проходит и попадает к апстриму ровно раз.
	w = e.do(t, http.MethodPost, "/api/browse/"+view.ID+"/tool", map[string]any{"name": "read_all"})
	status(t, w, http.StatusOK)
	if calls := readCalls(t, callLog); len(calls) != 1 || calls[0] != "read_all" {
		t.Fatalf("разрешённый вызов апстрима: %v", calls)
	}

	// Ресурс и промпт судятся тем же правилом, что чтение через прокси:
	// запрещённое имя — 403 без похода в апстрим, разрешённое проходит.
	for _, call := range []struct{ path, body, want, deny string }{
		{"/resource", `{"uri": "test://secret"}`, "test://secret", "секрет закрыт"},
		{"/prompt", `{"name": "secret_prompt"}`, "secret_prompt", "промпт закрыт"},
	} {
		w = e.do(t, http.MethodPost, "/api/browse/"+view.ID+call.path, json.RawMessage(call.body))
		status(t, w, http.StatusForbidden)
		if body := w.Body.String(); !strings.Contains(body, call.deny) {
			t.Fatalf("%s: в 403 нет текста правила: %s", call.path, body)
		}
		if calls := readCalls(t, callLog); len(calls) != 1 {
			t.Fatalf("%s: запрещённый вызов дошёл до апстрима: %v", call.path, calls)
		}
	}
	for _, call := range []struct{ path, body, want string }{
		{"/resource", `{"uri": "test://open"}`, "test://open"},
		{"/prompt", `{"name": "open_prompt"}`, "open_prompt"},
	} {
		w = e.do(t, http.MethodPost, "/api/browse/"+view.ID+call.path, json.RawMessage(call.body))
		status(t, w, http.StatusOK)
		if calls := readCalls(t, callLog); len(calls) == 0 || calls[len(calls)-1] != call.want {
			t.Fatalf("%s: разрешённый вызов апстрима: %v", call.path, calls)
		}
	}
}

func readCalls(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("лог вызовов: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// fixtureTool — дескриптор инструмента из фикстуры такой, каким его видит
// клиент; нужен, чтобы записать в файл настоящий хэш одобренной версии.
func fixtureTool(t *testing.T, exe, version, name string) *mcp.Tool {
	t.Helper()
	cmd := exec.Command(exe, "-test.run=TestBrowseBlocksChangedDescriptor$")
	cmd.Env = append(os.Environ(), "BROWSE_UPSTREAM=1", "BROWSE_TOOL_VERSION="+version)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "0"}, nil).
		Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("фикстура %s: %v", version, err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("список инструментов фикстуры: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("в фикстуре нет %q", name)
	return nil
}

// Browse обязан уважать карантин: дескриптор в открытой сессии разошёлся с
// одобренным — вызов не доходит до апстрима и получает 403, а листинг помечает
// инструмент changed.
func TestBrowseBlocksChangedDescriptor(t *testing.T) {
	if os.Getenv("BROWSE_UPSTREAM") == "1" {
		browseUpstream()
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(t.TempDir(), "calls.log")
	approved := toollock.Hash(fixtureTool(t, exe, "v1", "read_all"))

	e := newEnv(t, AuthOptions{Off: true})
	w := e.do(t, http.MethodPost, "/api/servers", map[string]any{
		"name":    "alpha",
		"command": []string{exe, "-test.run=TestBrowseBlocksChangedDescriptor$"},
		"env": map[string]string{
			"BROWSE_UPSTREAM": "1", "BROWSE_CALL_LOG": callLog, "BROWSE_TOOL_VERSION": "v2",
		},
	})
	status(t, w, http.StatusCreated)
	id := e.addSet(t, "quar", "alpha")
	if err := toollock.Write(toollock.Path(e.dir, id), toollock.File{Tools: map[string]toollock.Entry{
		"alpha/read_all": {Hash: approved, ApprovedAt: "2026-11-15T00:00:00Z", ApprovedBy: "auto"},
	}}); err != nil {
		t.Fatalf("запись карантина: %v", err)
	}

	w = e.do(t, http.MethodPost, "/api/browse", map[string]any{"server": "alpha"})
	status(t, w, http.StatusOK)
	var view browseView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("ответ browse: %v (%s)", err, w.Body.String())
	}
	changed := false
	for _, tool := range view.Tools {
		if tool.Name == "read_all" && tool.Changed {
			changed = true
		}
	}
	if !changed {
		t.Fatalf("изменившийся инструмент не помечен: %+v", view.Tools)
	}

	w = e.do(t, http.MethodPost, "/api/browse/"+view.ID+"/tool", map[string]any{"name": "read_all"})
	status(t, w, http.StatusForbidden)
	if !strings.Contains(w.Body.String(), "изменился") {
		t.Fatalf("отказ без пометки о смене: %s", w.Body.String())
	}
	if raw, err := os.ReadFile(callLog); err == nil && strings.Contains(string(raw), "read_all") {
		t.Fatalf("вызов дошёл до апстрима: %s", raw)
	}
}
