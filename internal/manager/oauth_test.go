package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

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
