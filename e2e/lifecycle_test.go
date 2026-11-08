package e2e

// Lifecycle suite: a real panel process supervises a real set process, which
// talks to a real stdio downstream. No E2E_BASE: everything is built and
// started here, so the gate runs without a prepared environment.
//
//	go test ./e2e -run TestLifecycle -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type lifecycle struct {
	t      *testing.T
	dir    string
	bin    string
	panel  string // base URL
	cmd    *exec.Cmd
	client *http.Client
}

func TestLifecycleStartServeStop(t *testing.T) {
	if testing.Short() {
		t.Skip("сборка бинарей и супервизия процессов — долгий сценарий")
	}
	l := newLifecycle(t)
	defer l.close()

	fixture := l.build("./cmd/testmcp", "testmcp")
	l.post("/api/servers", map[string]any{"name": "fix", "command": []string{fixture}}, http.StatusCreated)
	created := l.post("/api/sets", map[string]any{"name": "life", "servers": []string{"fix"}}, http.StatusCreated)
	port := int(created["port"].(float64))
	if port < 9100 || port > 9199 {
		t.Fatalf("порт набора %d вне 9100–9199", port)
	}
	key := l.post("/api/sets/life/keys", nil, http.StatusOK)
	token, _ := key["token"].(string)
	if len(token) != 48 {
		t.Fatalf("ключ не 48 hex: %q", token)
	}

	started := l.post("/api/sets/life/start", nil, http.StatusOK)
	if running, _ := started["running"].(bool); !running {
		t.Fatalf("набор не запустился: %v", started)
	}
	pid := int(started["pid"].(float64))
	if pid <= 0 {
		t.Fatalf("нет pid в ответе старта: %v", started)
	}

	// Настоящий MCP поверх настоящего прокси: мета-тулы свои, инструменты —
	// из живого stdio-процесса на другом конце.
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	l.mcpInit(url, token)
	meta := l.mcpCall(url, token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{},
	})
	for _, want := range []string{"list_servers", "list_commands", "describe_commands", "invoke_command"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("в мета-тулах нет %q: %s", want, meta)
		}
	}
	cmds := l.mcpCall(url, token, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name":      "list_commands",
			"arguments": map[string]any{"server": "fix"},
		},
	})
	for _, want := range []string{"echo", "fail"} {
		if !strings.Contains(cmds, want) {
			t.Fatalf("downstream не отдал инструмент %q: %s", want, cmds)
		}
	}

	health := l.get("/api/sets/life/servers")
	if running, _ := health["running"].(bool); !running {
		t.Fatalf("health не видит запущенный набор: %v", health)
	}
	if servers, ok := health["servers"].([]any); ok {
		for _, s := range servers {
			if m, ok := s.(map[string]any); ok {
				if healthy, ok := m["healthy"].(bool); ok && !healthy {
					t.Fatalf("сервер %v нездоров: %v", m["name"], m["error"])
				}
			}
		}
	}

	// Счётчик считает только реальные вызовы инструмента.
	l.mcpCall(url, token, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{
			"name":      "invoke_command",
			"arguments": map[string]any{"server": "fix", "command_name": "echo", "parameters": map[string]any{"text": "e2e"}},
		},
	})
	stats := l.getSetStats(port, token)
	if calls, _ := stats["counters"].([]any); len(calls) == 0 {
		t.Fatalf("/stats пуст после вызова tools/list: %v", stats)
	}

	stopped := l.post("/api/sets/life/stop", nil, http.StatusOK)
	if running, _ := stopped["running"].(bool); running {
		t.Fatalf("набор не остановился: %v", stopped)
	}
	if alive := portOpen(port, 5*time.Second); alive {
		t.Fatalf("порт %d всё ещё слушает после stop", port)
	}
	if processAlive(pid) {
		t.Fatalf("процесс набора %d жив после stop", pid)
	}
}

func TestLifecycleRestartAfterKill(t *testing.T) {
	if testing.Short() {
		t.Skip("супервизия и backoff — долгий сценарий")
	}
	l := newLifecycle(t)
	defer l.close()

	fixture := l.build("./cmd/testmcp", "testmcp")
	l.post("/api/servers", map[string]any{"name": "fix", "command": []string{fixture}}, http.StatusCreated)
	l.post("/api/sets", map[string]any{"name": "rest", "servers": []string{"fix"}}, http.StatusCreated)
	started := l.post("/api/sets/rest/start", nil, http.StatusOK)
	first := int(started["pid"].(float64))

	// Убиваем процесс набора мимо панели: супервизия обязана поднять его снова.
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatalf("kill %d: %v", first, err)
	}

	deadline := time.Now().Add(45 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = l.findSet("rest")
		rt, _ := last["runtime"].(map[string]any)
		running, _ := rt["running"].(bool)
		pid, _ := rt["pid"].(float64)
		restarts, _ := rt["restarts"].(float64)
		if running && int(pid) != first && restarts >= 1 {
			// Панель не имеет права оставить набор сиротой.
			second := int(pid)
			l.close()
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if !processAlive(second) {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("после завершения панели процесс набора %d остался жив", second)
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("набор не поднялся после kill за 45 с: %v", last)
}

// ─── harness ─────────────────────────────────────────────────────────────────

func newLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	dir := t.TempDir()
	l := &lifecycle{t: t, dir: dir, client: &http.Client{Timeout: 60 * time.Second}}
	l.bin = l.build("./cmd/mcprig", "mcprig")
	l.panel = fmt.Sprintf("http://127.0.0.1:%d", freePort(t))
	l.start()
	return l
}

func (l *lifecycle) build(pkg, name string) string {
	l.t.Helper()
	out := filepath.Join(l.dir, name)
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = ".."
	if b, err := cmd.CombinedOutput(); err != nil {
		l.t.Fatalf("сборка %s: %v\n%s", pkg, err, b)
	}
	return out
}

func (l *lifecycle) start() {
	l.t.Helper()
	cmd := exec.Command(l.bin, "panel")
	cmd.Env = append(os.Environ(),
		"MCPRIG_DATA_DIR="+filepath.Join(l.dir, "data"),
		"MCPRIG_PORT="+strings.TrimPrefix(l.panel, "http://127.0.0.1:"),
		"MCPRIG_AUTH=off",
		"MCPRIG_CLI="+l.bin,
		"MCPRIG_BIND_IP=127.0.0.1",
	)
	log, err := os.Create(filepath.Join(l.dir, "panel.log"))
	if err != nil {
		l.t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		l.t.Fatalf("старт панели: %v", err)
	}
	l.cmd = cmd

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := l.client.Get(l.panel + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(l.dir, "panel.log"))
	l.t.Fatalf("панель не ответила за 20 с: %s", b)
}

func (l *lifecycle) close() {
	if l.cmd != nil && l.cmd.Process != nil {
		defer func() { l.cmd = nil }()
		l.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { l.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			l.cmd.Process.Kill()
		}
	}
}

func (l *lifecycle) findSet(id string) map[string]any {
	l.t.Helper()
	state := l.getBody("/api/sets")
	raw, _ := json.Marshal(state)
	var sets []map[string]any
	_ = json.Unmarshal(raw, &sets)
	for _, set := range sets {
		if set["id"] == id {
			return set
		}
	}
	l.t.Fatalf("в списке нет набора %q: %s", id, raw)
	return nil
}

func (l *lifecycle) getBody(path string) any {
	l.t.Helper()
	resp, err := l.client.Get(l.panel + path)
	if err != nil {
		l.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		l.t.Fatalf("GET %s: не JSON (%d): %s", path, resp.StatusCode, body)
	}
	if resp.StatusCode != http.StatusOK {
		l.t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
	}
	return out
}

func (l *lifecycle) get(path string) map[string]any {
	l.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, l.panel+path, nil)
	resp, err := l.client.Do(req)
	if err != nil {
		l.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		l.t.Fatalf("GET %s: не JSON (%d): %s", path, resp.StatusCode, body)
	}
	if resp.StatusCode != http.StatusOK {
		l.t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
	}
	return out
}

func (l *lifecycle) post(path string, payload any, want int) map[string]any {
	l.t.Helper()
	var body io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(http.MethodPost, l.panel+path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := l.client.Do(req)
	if err != nil {
		l.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != want {
		l.t.Fatalf("POST %s: %d (ждали %d): %s", path, resp.StatusCode, want, raw)
	}
	return out
}

// mcpInit проходит initialize по HTTP-транспорту набора.
func (l *lifecycle) mcpInit(url, token string) string {
	l.t.Helper()
	init := l.mcpCall(url, token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "e2e", "version": "1"},
		},
	})
	if !strings.Contains(init, "protocolVersion") {
		l.t.Fatalf("initialize не ответил: %s", init)
	}
	return init
}

// postAny принимает любой не-5xx-код: некоторые вызовы допустимо завершать
// по-разному (старт падающего набора).
func (l *lifecycle) postAny(path string, payload any) map[string]any {
	l.t.Helper()
	var body io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(http.MethodPost, l.panel+path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := l.client.Do(req)
	if err != nil {
		l.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 500 && resp.StatusCode != http.StatusInternalServerError {
		l.t.Fatalf("POST %s: %d %s", path, resp.StatusCode, raw)
	}
	return out
}

func (l *lifecycle) mcpCall(url, token string, payload map[string]any) string {
	l.t.Helper()
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := l.client.Do(req)
	if err != nil {
		l.t.Fatalf("MCP %v: %v", payload["method"], err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		l.t.Fatalf("MCP %v: %d %s", payload["method"], resp.StatusCode, body)
	}
	return string(body)
}

func (l *lifecycle) getSetStats(port int, token string) map[string]any {
	l.t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/stats", port)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := l.client.Do(req)
	if err != nil {
		l.t.Fatalf("GET /stats: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		l.t.Fatalf("/stats: не JSON (%d): %s", resp.StatusCode, body)
	}
	return out
}

func (l *lifecycle) writeScript(name, body string) string {
	l.t.Helper()
	path := filepath.Join(l.dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		l.t.Fatal(err)
	}
	return path
}

func portOpen(port int, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err != nil {
			return false
		}
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

func processAlive(pid int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "kill", "-0", fmt.Sprint(pid)).Run(); err != nil {
		return false
	}
	return true
}
