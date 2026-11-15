package manager

// Ворота ставки D: смена основного ключа не откатывает профили. Набор здесь —
// настоящий бинарь, потому что профиль ключа исполняет сам набор: фикстура
// токенов не проверяет и профиль не применит.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realCLI собирает настоящий mcprig: подмена фикстурой здесь не годится.
func realCLI(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("поднимает набор настоящим бинарём")
	}
	out := filepath.Join(t.TempDir(), "mcprig")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/mcprig")
	cmd.Dir = "../.."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("сборка mcprig: %v\n%s", err, b)
	}
	return out
}

// newEnvCLI — env, у которого набор запускается настоящим бинарём.
func newEnvCLI(t *testing.T, cli string) *env {
	t.Helper()
	dir := t.TempDir()
	m, err := New(Options{
		DataDir: dir,
		Version: "test",
		CLIPath: cli,
		Auth:    AuthOptions{Off: true, AllowedHosts: []string{"example.com", "127.0.0.1"}},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)
	return &env{m: m, dir: dir}
}

// setCall зовёт инструмент набора по HTTP и отдаёт текст ответа.
func setCall(t *testing.T, port int, token, tool string, args map[string]any) string {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/mcp", port), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("вызов %s: %v", tool, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вызов %s: %d %s", tool, resp.StatusCode, body)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			body = []byte(data)
		}
	}
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("ответ %s: %v (%s)", tool, err, body)
	}
	if len(out.Result.Content) == 0 {
		t.Fatalf("ответ %s без содержимого: %s", tool, body)
	}
	return out.Result.Content[0].Text
}

// Смена основного ключа через PUT применяется reload'ом и сохраняет профиль:
// прежний код считал authToken полем «только перезапуском» и возвращал старую
// таблицу профилей, то есть ключ получал полные права.
func TestPrimaryKeyRotationKeepsProfile(t *testing.T) {
	e := newEnvCLI(t, realCLI(t))
	e.addServer(t, "a")
	id := e.addSet(t, "rot", "a")

	w := e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{
		"profiles": map[string]any{"reader": map[string]any{"deny": []string{"danger"}}},
	})
	status(t, w, http.StatusOK)

	w = e.do(t, http.MethodGet, "/api/sets/"+id+"/keys", nil)
	status(t, w, http.StatusOK)
	var keyList struct {
		Keys []struct {
			ID string `json:"id"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &keyList); err != nil {
		t.Fatalf("список ключей: %v (%s)", err, w.Body.String())
	}
	// Профиль живёт в ключе, а PUT {"token"} меняет токен первого действующего
	// ключа: чтобы ротация задела профильный ключ, прежний ключ набора отзываем.
	for _, key := range keyList.Keys {
		status(t, e.do(t, http.MethodDelete, "/api/sets/"+id+"/keys/"+key.ID, nil), http.StatusOK)
	}

	w = e.do(t, http.MethodPost, "/api/sets/"+id+"/keys", map[string]any{"profile": "reader"})
	status(t, w, http.StatusOK)
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil || issued.Token == "" {
		t.Fatalf("ключ не выдан: %v (%s)", err, w.Body.String())
	}

	w = e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	started := decode[RuntimeStatus](t, w)
	if !started.Running || started.Port == 0 {
		t.Fatalf("набор не поднялся: %+v", started)
	}

	danger := map[string]any{"server": "a", "command_name": "danger", "parameters": map[string]any{}}
	if text := setCall(t, started.Port, issued.Token, "invoke_command", danger); !strings.Contains(text, "Blocked by mcprig policy") {
		t.Fatalf("профиль ключа не ограничил вызов: %q", text)
	}

	// Ротация: тот же ключ с новым токеном, профиль прежний.
	w = e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{"token": "rotated-key"})
	status(t, w, http.StatusOK)
	var view struct {
		ReloadError string `json:"reloadError"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("ответ правки: %v (%s)", err, w.Body.String())
	}
	if view.ReloadError != "" {
		t.Fatalf("правка ключа дала ложную ошибку перезагрузки: %q", view.ReloadError)
	}

	if text := setCall(t, started.Port, "rotated-key", "invoke_command", danger); !strings.Contains(text, "Blocked by mcprig policy") {
		t.Fatalf("после ротации ключа профиль потерян: %q", text)
	}
}
