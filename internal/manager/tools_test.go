package manager

// Маршруты карантина и чистка файла: панель читает записи набора, одобряет
// текущий дескриптор, а сервер, ушедший из набора, не оставляет записей.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akrhin/mcprig/internal/toollock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolsApproveEndpoint: изменившийся инструмент виден как changed, после
// POST approve он одобрен, а ответ называет, что именно одобрено.
func TestToolsApproveEndpoint(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "alpha")
	id := e.addSet(t, "quar", "alpha")
	path := toollock.Path(e.dir, id)
	// Хэши настоящие: так запись и появилась бы у процесса набора.
	v1 := toollock.Hash(&mcp.Tool{Name: "echo", Description: "v1"})
	v2 := toollock.Hash(&mcp.Tool{Name: "echo", Description: "v2"})
	if err := toollock.Write(path, toollock.File{Tools: map[string]toollock.Entry{
		"alpha/echo": {Hash: v1, Seen: v2, ApprovedAt: "2026-11-15T00:00:00Z", ApprovedBy: "auto"},
		"alpha/ping": {Hash: toollock.Hash(&mcp.Tool{Name: "ping"})},
	}}); err != nil {
		t.Fatalf("запись карантина: %v", err)
	}

	w := e.do(t, http.MethodGet, "/api/sets/"+id+"/tools", nil)
	status(t, w, http.StatusOK)
	list := decode[struct {
		Tools []toolView `json:"tools"`
	}](t, w)
	if len(list.Tools) != 2 || list.Tools[0].Tool != "echo" || list.Tools[0].State != "changed" {
		t.Fatalf("список не тот: %+v", list.Tools)
	}
	if list.Tools[0].Hash != v2 || list.Tools[0].ApprovedHash != v1 {
		t.Fatalf("хэши не те: %+v", list.Tools[0])
	}
	if list.Tools[1].State != "ok" {
		t.Fatalf("неизменившийся инструмент помечен: %+v", list.Tools[1])
	}

	w = e.do(t, http.MethodPost, "/api/sets/"+id+"/tools/approve", map[string]any{"server": "alpha", "tool": "echo"})
	status(t, w, http.StatusOK)
	approved := decode[struct {
		Approved []string `json:"approved"`
	}](t, w)
	if len(approved.Approved) != 1 || approved.Approved[0] != "alpha/echo" {
		t.Fatalf("одобрено не то: %+v", approved.Approved)
	}
	if got := toollock.Read(path).Tools["alpha/echo"]; got.Hash != v2 || got.Seen != "" || got.ApprovedBy != "panel" {
		t.Fatalf("одобрение не записано: %+v", got)
	}

	// Повторное одобрение — не ошибка: панель может нажать кнопку дважды.
	w = e.do(t, http.MethodPost, "/api/sets/"+id+"/tools/approve", map[string]any{"all": true})
	status(t, w, http.StatusOK)
	if empty := decode[struct {
		Approved []string `json:"approved"`
	}](t, w); len(empty.Approved) != 0 {
		t.Fatalf("одобрять было нечего, а ответил: %+v", empty.Approved)
	}
}

// TestRemovedServerLeavesNoLockEntries: сборка конфига набора без сервера
// чистит его записи — иначе файл рос бы вечно.
func TestRemovedServerLeavesNoLockEntries(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"servers":[{"name":"alpha","transport":"stdio","command":["downstream"]}]}`)
	path := toollock.Path(dir, "main")
	if err := toollock.Write(path, toollock.File{Tools: map[string]toollock.Entry{
		"alpha/echo": {Hash: "a"},
		"beta/echo":  {Hash: "b"},
	}}); err != nil {
		t.Fatalf("запись карантина: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := WriteSetConfig(dir, "main", body); err != nil {
			t.Fatalf("запись конфига: %v", err)
		}
		tools := toollock.Read(path).Tools
		if _, ok := tools["beta/echo"]; ok {
			t.Fatalf("запись ушедшего сервера осталась: %+v", tools)
		}
		if _, ok := tools["alpha/echo"]; !ok {
			t.Fatalf("запись оставшегося сервера пропала: %+v", tools)
		}
	}
}

// TestToolsApproveWithAuth: панель с паролем отбивает POST с сессионной cookie
// без Origin, и CLI обязан слать Origin как браузер — здесь проверяется, что с
// ним одобрение проходит.
func TestToolsApproveWithAuth(t *testing.T) {
	e := newEnv(t, AuthOptions{Password: "pw"})
	const origin = "http://example.com"
	w := e.do(t, http.MethodPost, "/api/login", map[string]any{"user": "admin", "password": "pw"})
	status(t, w, http.StatusOK)
	cookie := w.Result().Cookies()[0]
	asAdmin := func(body any) *httptest.ResponseRecorder {
		t.Helper()
		return e.doWithCookieOrigin(t, http.MethodPost, "/api/servers", body, cookie, origin)
	}
	status(t, asAdmin(map[string]any{"name": "alpha", "command": []string{"downstream"}}), http.StatusCreated)
	w = e.doWithCookieOrigin(t, http.MethodPost, "/api/sets",
		map[string]any{"name": "quar", "servers": []string{"alpha"}}, cookie, origin)
	status(t, w, http.StatusCreated)
	id := decode[struct {
		ID string `json:"id"`
	}](t, w).ID
	path := toollock.Path(e.dir, id)
	v1 := toollock.Hash(&mcp.Tool{Name: "echo", Description: "v1"})
	v2 := toollock.Hash(&mcp.Tool{Name: "echo", Description: "v2"})
	if err := toollock.Write(path, toollock.File{Tools: map[string]toollock.Entry{
		"alpha/echo": {Hash: v1, Seen: v2, ApprovedBy: "auto"},
	}}); err != nil {
		t.Fatalf("запись карантина: %v", err)
	}

	body := map[string]any{"server": "alpha", "tool": "echo"}
	status(t, e.doWithCookieOrigin(t, http.MethodPost, "/api/sets/"+id+"/tools/approve", body, cookie, ""), http.StatusForbidden)
	w = e.doWithCookieOrigin(t, http.MethodPost, "/api/sets/"+id+"/tools/approve", body, cookie, origin)
	status(t, w, http.StatusOK)
	if got := toollock.Read(path).Tools["alpha/echo"]; got.Hash != v2 || got.Seen != "" {
		t.Fatalf("одобрение не записано: %+v", got)
	}
}

// shortcut: рестарт настоящего процесса набора не проверяется отдельным e2e —
// файл одобрений лежит в sets/<id> и перезапуск его не трогает
// (TestLockFilePersistsAcrossRestart в internal/mcpserver проверяет смену
// менеджера целиком); жизненный цикл процесса набора покрыт тестами runtime.
