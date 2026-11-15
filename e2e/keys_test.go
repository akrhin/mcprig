package e2e

// Keys suite: a set serves several active keys at once. A new key arrives by
// reload (SIGHUP), so the set process keeps its pid, and each key keeps its own
// profile: two keys with different profiles see different catalogs.
//
//	go test ./e2e -run 'TestTokenListAppliedWithoutRestart|TestProfilePerKeyAfterReload' -v

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestTokenListAppliedWithoutRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("сборка бинарей и супервизия процессов — долгий сценарий")
	}
	l := newLifecycle(t)
	defer l.close()

	fixture := l.build("./cmd/testmcp", "testmcp")
	l.post("/api/servers", map[string]any{"name": "fix", "command": []string{fixture}}, http.StatusCreated)
	created := l.post("/api/sets", map[string]any{"name": "keys", "servers": []string{"fix"}}, http.StatusCreated)
	first, _ := created["token"].(string)
	if len(first) != 48 {
		t.Fatalf("ключ набора не 48 hex: %q", first)
	}
	started := l.post("/api/sets/keys/start", nil, http.StatusOK)
	pid := int(started["pid"].(float64))

	// Второй ключ выдаётся работающему набору: оба токена обязаны работать
	// сразу, а процесс набора остаться тем же.
	issued := l.post("/api/sets/keys/keys", map[string]any{"label": "second"}, http.StatusOK)
	second, _ := issued["token"].(string)
	if len(second) != 48 || second == first {
		t.Fatalf("второй ключ = %q", second)
	}
	view, _ := issued["set"].(map[string]any)
	runtime, _ := view["runtime"].(map[string]any)
	if got, _ := runtime["pid"].(float64); int(got) != pid {
		t.Fatalf("набор перезапущен: pid %d → %v", pid, runtime["pid"])
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", int(created["port"].(float64)))
	for name, token := range map[string]string{"прежний": first, "новый": second} {
		l.mcpInit(url, token)
		catalog := l.mcpCall(url, token, map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{
				"name":      "list_commands",
				"arguments": map[string]any{"server": "fix"},
			},
		})
		if !strings.Contains(catalog, "echo") {
			t.Fatalf("ключ %s не получил каталог: %s", name, catalog)
		}
	}
}

func TestProfilePerKeyAfterReload(t *testing.T) {
	if testing.Short() {
		t.Skip("сборка бинарей и супервизия процессов — долгий сценарий")
	}
	l := newLifecycle(t)
	defer l.close()

	fixture := l.build("./cmd/testmcp", "testmcp")
	for _, name := range []string{"alpha", "beta"} {
		l.post("/api/servers", map[string]any{"name": name, "command": []string{fixture}}, http.StatusCreated)
	}
	created := l.post("/api/sets", map[string]any{
		"name":     "prof",
		"servers":  []string{"alpha", "beta"},
		"profiles": map[string]any{"only-alpha": map[string]any{"allow": []string{"alpha/*"}}},
	}, http.StatusCreated)
	port := int(created["port"].(float64))

	// Первый ключ — с профилем, второй выдаётся работающему набору без профиля
	// в записи ключа, но с профилем only-alpha: профиль едет в reload.
	alphaKey := l.post("/api/sets/prof/keys", map[string]any{"profile": "only-alpha"}, http.StatusOK)
	alphaToken, _ := alphaKey["token"].(string)
	l.post("/api/sets/prof/start", nil, http.StatusOK)

	wide := l.post("/api/sets/prof/keys", map[string]any{"label": "wide"}, http.StatusOK)
	wideToken, _ := wide["token"].(string)

	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	catalog := func(token string) map[string][]string {
		l.mcpInit(url, token)
		out := l.mcpCall(url, token, map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{
				"name": "list_commands",
				"arguments": map[string]any{
					"servers": []string{"alpha", "beta"}, "namesOnly": true,
				},
			},
		})
		var got map[string][]string
		if err := json.Unmarshal([]byte(contentText(t, out)), &got); err != nil {
			t.Fatalf("list_commands не JSON: %v: %s", err, out)
		}
		return got
	}

	narrow := catalog(alphaToken)
	if len(narrow["alpha"]) == 0 {
		t.Fatalf("ключ с профилем не видит alpha: %v", narrow)
	}
	if len(narrow["beta"]) != 0 {
		t.Fatalf("профиль ключа пропустил beta: %v", narrow)
	}
	open := catalog(wideToken)
	if len(open["alpha"]) == 0 || len(open["beta"]) == 0 {
		t.Fatalf("ключ без профиля не видит оба сервера: %v", open)
	}
}

// contentText достаёт text из ответа набора: MCP отдаёт либо
// application/json, либо одно событие text/event-stream, а полезная нагрузка
// лежит в result.content[0].text.
func contentText(t *testing.T, body string) string {
	t.Helper()
	if i := strings.Index(body, "data: "); i >= 0 {
		body = strings.TrimSpace(body[i+len("data: "):])
	}
	var reply struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("ответ MCP не JSON: %v: %s", err, body)
	}
	if len(reply.Result.Content) == 0 {
		t.Fatalf("пустой ответ MCP: %s", body)
	}
	return reply.Result.Content[0].Text
}
