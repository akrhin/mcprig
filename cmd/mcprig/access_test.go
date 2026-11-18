package main

// Ворота ставки E: политика доступа живого слушателя меняется reload'ом, а не
// перезапуском набора. Тесты идут через reloadOnce — тот же путь, что у serve.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/httplisten"
	"github.com/akrhin/mcprig/internal/mcpserver"
	"github.com/akrhin/mcprig/internal/stats"
)

type accessEnv struct {
	t       *testing.T
	path    string
	mgr     *mcpserver.Manager
	handler *httplisten.Server
	rec     *stats.Recorder
}

// newAccessEnv повторяет сборку serve: менеджер, слушатель и инструмент who,
// который отдаёт метку клиента и пишет её в счётчики.
func newAccessEnv(t *testing.T, transport string) *accessEnv {
	t.Helper()
	e := &accessEnv{t: t, path: filepath.Join(t.TempDir(), "servers.json"), rec: stats.NewRecorder("set", 0)}
	e.write(transport)

	cfg, err := config.Load(e.path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e.mgr = mcpserver.New(cfg, "test", nil)
	t.Cleanup(func() { _ = e.mgr.Close() })
	srv := e.mgr.Server()
	srv.AddTool(&mcp.Tool{Name: "who", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			client := stats.ClientFrom(ctx, "who")
			e.rec.Record(client, "up", "who", 0, nil)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: client}}}, nil
		})
	handler, err := httplisten.New(cfg, srv, quietLog(), e.rec)
	if err != nil {
		t.Fatalf("httplisten.New: %v", err)
	}
	e.handler = handler

	return e
}

func (e *accessEnv) write(transport string) {
	e.t.Helper()
	writeServers(e.t, e.path, `{"servers": [], "transport": `+transport+`}`)
}

// reload меняет файл и гоняет ту же перезагрузку, что SIGHUP на живом наборе.
func (e *accessEnv) reload() {
	e.t.Helper()
	if res := reloadOnce(e.mgr, e.handler, e.path, quietLog()); res.Err != "" {
		e.t.Fatalf("reload: %+v", res)
	}
}

// statsPanel забирает снимок так, как это делает панель: набор отдаёт метки
// клиентов и тексты ошибок только служебному вызову.
func (e *accessEnv) statsPanel(auth string) (int, stats.Snapshot) {
	e.t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/stats", nil)
	r.Host = "127.0.0.1"
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	r.Header.Set(stats.ServiceCallHeader, stats.ServiceCallerPanel)
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	var snap stats.Snapshot
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
			e.t.Fatalf("снимок: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, snap
}

// call зовёт who через /mcp и отдаёт текст ответа.
func (e *accessEnv) call(auth string) (int, string) {
	e.t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"who","arguments":{}}}`
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Host = "127.0.0.1"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		return w.Code, w.Body.String()
	}
	return w.Code, textOf(e.t, w.Body.String())
}

// textOf достаёт текст из ответа: сервер отдаёт JSON, клиент с Accept SSE —
// поток, где полезная строка идёт после "data: ".
func textOf(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if payload, ok := strings.CutPrefix(line, "data: "); ok {
			body = payload
		}
	}
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("ответ не разобран: %v (%s)", err, body)
	}
	if len(out.Result.Content) == 0 {
		t.Fatalf("ответ без содержимого: %s", body)
	}
	return out.Result.Content[0].Text
}

// Открытый набор получил первый ключ: анонимный клиент отсекается без
// перезапуска, а сам ключ работает.
func TestOpenSetKeyIssueKeepsAnonymous(t *testing.T) {
	e := newAccessEnv(t, `{"type": "http", "port": 9107, "allowAnonymous": true}`)
	if code, _ := e.call(""); code != http.StatusOK {
		t.Fatalf("открытый набор не пустил без заголовка: %d", code)
	}

	e.write(`{"type": "http", "port": 9107, "authTokens": ["k1"]}`)
	e.reload()

	if code, body := e.call(""); code != http.StatusUnauthorized {
		t.Fatalf("набор с ключом пустил без заголовка: %d %s", code, body)
	}
	if code, body := e.call("k1"); code != http.StatusOK {
		t.Fatalf("новый ключ не работает: %d %s", code, body)
	}
}

// Отзыв последнего ключа открытого набора возвращает его в открытое состояние.
func TestOpenSetLastKeyRevokeStaysOpen(t *testing.T) {
	e := newAccessEnv(t, `{"type": "http", "port": 9108, "authTokens": ["k1"]}`)
	if code, body := e.call(""); code != http.StatusUnauthorized {
		t.Fatalf("набор с ключом пустил без заголовка: %d %s", code, body)
	}

	e.write(`{"type": "http", "port": 9108, "allowAnonymous": true}`)
	e.reload()

	if code, body := e.call(""); code != http.StatusOK {
		t.Fatalf("открытый набор не пустил без заголовка: %d %s", code, body)
	}
	if code, _ := e.call("k1"); code != http.StatusUnauthorized {
		t.Fatalf("отозванный ключ принят: %d", code)
	}
}

// Подпись ключа из clientNames доезжает до /stats reload'ом, а не перезапуском.
func TestIssuedLabelAppliesWithoutRestart(t *testing.T) {
	e := newAccessEnv(t, `{"type": "http", "port": 9109, "authTokens": ["k1"]}`)
	if code, body := e.call("k1"); code != http.StatusOK || body == "" {
		t.Fatalf("вызов до правки: %d %q", code, body)
	}

	label := "панель"
	e.write(`{"type": "http", "port": 9109, "authTokens": ["k1"],
		"clientNames": {"` + stats.Fingerprint("k1") + `": "` + label + `"}}`)
	e.reload()

	if code, body := e.call("k1"); code != http.StatusOK || body != label {
		t.Fatalf("метка ключа не применилась: %d %q", code, body)
	}
	code, snap := e.statsPanel("k1")
	if code != http.StatusOK {
		t.Fatalf("stats: %d", code)
	}
	for _, c := range snap.Counters {
		if c.Client == label {
			return
		}
	}
	t.Fatalf("подписи %q нет в счётчиках: %+v", label, snap.Counters)
}
