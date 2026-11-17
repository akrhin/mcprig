package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// SEC-07: вход перезапускает набор, поэтому запуск — только POST. GET получает
// 405, состояние не меняется и набор не перезапускается; панель опрашивает
// состояние отдельным GET .../authorize/status.
func TestAuthorizeIsPostOnly(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	addServer(t, e, `{"name":"remote","transport":"http","url":"http://127.0.0.1:1/mcp","enabled":true}`)
	set, err := e.m.store.AddSet(json.RawMessage(`{"id":"s1","name":"s1","servers":["remote"]}`))
	if err != nil {
		t.Fatalf("AddSet: %v", err)
	}
	key := authKey("s1", "remote")
	before := e.m.rt.status(set)

	rec := e.do(t, http.MethodGet, "/api/sets/s1/servers/remote/authorize", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: код %d, ожидался 405: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow: %q, ожидался POST", got)
	}
	if st := e.m.flowView(key); st.State != "idle" {
		t.Fatalf("GET завёл вход: %+v", st)
	}
	if after := e.m.rt.status(set); after.Running != before.Running || after.PID != before.PID {
		t.Fatalf("GET перезапустил набор: было %+v, стало %+v", before, after)
	}

	rec = e.do(t, http.MethodGet, "/api/sets/s1/servers/remote/authorize/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status: код %d, ожидался 200: %s", rec.Code, rec.Body)
	}

	rec = e.do(t, http.MethodPost, "/api/sets/s1/servers/remote/authorize", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST: код %d, ожидался 202: %s", rec.Code, rec.Body)
	}
	var got authFlow
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ POST: %v (%s)", err, rec.Body)
	}
	if got.State != "pending" {
		t.Fatalf("POST не завёл вход: %+v", got)
	}

	// Горутина входа пишет токен в каталог данных: без ожидания t.TempDir
	// не удалится, а тест прочитает чужой незакрытый поток.
	deadline := time.Now().Add(5 * time.Second)
	for e.m.flowView(key).State == "pending" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if st := e.m.flowView(key); st.State == "pending" {
		t.Fatalf("вход не завершился за 5 с: %+v", st)
	}
}

// addServer кладёт сервер в реестр так же, как это делает панель.
func addServer(t *testing.T, e *env, raw string) {
	t.Helper()
	if _, err := e.m.store.AddServer(json.RawMessage(raw)); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
}

func TestAuthorizeRejectsStdioServer(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	addServer(t, e, `{"name":"local","transport":"stdio","command":["true"],"enabled":true}`)
	if _, err := e.m.store.AddSet(json.RawMessage(`{"id":"s1","name":"s1","servers":["local"]}`)); err != nil {
		t.Fatalf("AddSet: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/api/sets/s1/servers/local/authorize", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400: %s", rec.Code, rec.Body)
	}
}

func TestAuthorizeRequiresMembership(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	addServer(t, e, `{"name":"remote","transport":"http","url":"https://example.test/mcp","enabled":true}`)
	addServer(t, e, `{"name":"other","transport":"http","url":"https://other.test/mcp","enabled":true}`)
	if _, err := e.m.store.AddSet(json.RawMessage(`{"id":"s1","name":"s1","servers":["other"]}`)); err != nil {
		t.Fatalf("AddSet: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/api/sets/s1/servers/remote/authorize", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400: %s", rec.Code, rec.Body)
	}
}

// Опрос состояния проверяет то же, что запуск: сервер, выброшенный из набора,
// иначе отвечает 200 idle, и диалог панели крутится вечно.
func TestAuthorizeStatusRequiresMembership(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	addServer(t, e, `{"name":"remote","transport":"http","url":"https://example.test/mcp","enabled":true}`)
	addServer(t, e, `{"name":"other","transport":"http","url":"https://other.test/mcp","enabled":true}`)
	if _, err := e.m.store.AddSet(json.RawMessage(`{"id":"s1","name":"s1","servers":["other"]}`)); err != nil {
		t.Fatalf("AddSet: %v", err)
	}
	rec := e.do(t, http.MethodGet, "/api/sets/s1/servers/remote/authorize/status", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400: %s", rec.Code, rec.Body)
	}
	if msg := decode[map[string]string](t, rec)["error"]; !strings.Contains(msg, "не входит в набор") {
		t.Fatalf("ошибка = %q", msg)
	}
}

func TestAuthCallbackRejectsUnknownState(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	rec := e.do(t, http.MethodGet, "/oauth/callback?state=nope&code=xyz", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400", rec.Code)
	}
}

func TestAuthorizationFlowDeliversCode(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got := make(chan *authOutcome, 1)
	go func() {
		res, err := e.m.waitAuthorization(ctx, authKey("s1", "remote"), "https://as.test/authorize?state=st1&client_id=c")
		got <- &authOutcome{res: res, err: err}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for e.m.flowView(authKey("s1", "remote")).URL == "" {
		if time.Now().After(deadline) {
			t.Fatalf("ссылка не попала в состояние входа: %+v", e.m.flowView(authKey("s1", "remote")))
		}
		time.Sleep(10 * time.Millisecond)
	}

	rec := e.do(t, http.MethodGet, "/oauth/callback?state=st1&code=the-code", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200: %s", rec.Code, rec.Body)
	}
	out := <-got
	if out.err != nil {
		t.Fatalf("waitAuthorization: %v", out.err)
	}
	if out.res.Code != "the-code" || out.res.State != "st1" {
		t.Fatalf("код %q, state %q", out.res.Code, out.res.State)
	}
}

func TestCallbackURLFollowsPanelAddress(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.m.opts.Host = "192.168.1.92"
	e.m.opts.Port = 9088
	got, err := e.m.callbackURL()
	if err != nil {
		t.Fatalf("callbackURL: %v", err)
	}
	if want := "http://192.168.1.92:9088/oauth/callback"; got != want {
		t.Fatalf("получено %q, ожидалось %q", got, want)
	}
}
