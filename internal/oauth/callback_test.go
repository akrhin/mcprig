package oauth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newTestCallback(t *testing.T, state string) *Callback {
	t.Helper()
	cb, err := NewCallback("127.0.0.1:0", state)
	if err != nil {
		t.Fatalf("NewCallback: %v", err)
	}
	t.Cleanup(func() { cb.Close() })
	return cb
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestCallbackSuccess(t *testing.T) {
	cb := newTestCallback(t, "st-1")
	if !strings.HasPrefix(cb.URL(), "http://127.0.0.1:") || !strings.HasSuffix(cb.URL(), "/callback") {
		t.Fatalf("URL = %q", cb.URL())
	}

	type outcome struct {
		res *Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := cb.Wait(context.Background())
		ch <- outcome{res, err}
	}()
	waitUntilWaiting(t, cb)

	code, body := get(t, cb.URL()+"?code=abc&state=st-1&iss=https%3A%2F%2Fas.example")
	if code != http.StatusOK {
		t.Fatalf("статус = %d, want 200", code)
	}
	if !strings.Contains(body, "Авторизация завершена") {
		t.Fatalf("нет страницы успеха: %q", body)
	}

	select {
	case o := <-ch:
		if o.err != nil {
			t.Fatalf("Wait: %v", o.err)
		}
		if o.res.Code != "abc" || o.res.State != "st-1" || o.res.Iss != "https://as.example" {
			t.Fatalf("Result = %+v", o.res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait не завершился")
	}
}

// Чужой state — 400, но ожидание не снимается: иначе любой посторонний
// редирект гасит вход, открытый в браузере владельца.
func TestCallbackStateMismatch(t *testing.T) {
	cb := newTestCallback(t, "st-1")
	type outcome struct {
		res *Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := cb.Wait(context.Background())
		ch <- outcome{res, err}
	}()
	waitUntilWaiting(t, cb)

	code, _ := get(t, cb.URL()+"?code=abc&state=wrong")
	if code != http.StatusBadRequest {
		t.Fatalf("статус = %d, want 400", code)
	}
	select {
	case o := <-ch:
		t.Fatalf("чужой state снял ожидание: %+v", o)
	case <-time.After(100 * time.Millisecond):
	}

	// Верный редирект после чужого по-прежнему доходит.
	if code, _ := get(t, cb.URL()+"?code=abc&state=st-1"); code != http.StatusOK {
		t.Fatalf("верный редирект: статус = %d, want 200", code)
	}
	select {
	case o := <-ch:
		if o.err != nil || o.res.Code != "abc" {
			t.Fatalf("Wait: %+v", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait не завершился")
	}
}

// Ошибка авторизации с чужим state — тоже чужой редирект: 400 без снятия
// ожидания. Иначе достаточно подсунуть ?error=, чтобы сорвать вход.
func TestCallbackForeignErrorKeepsWaiting(t *testing.T) {
	cb := newTestCallback(t, "st-1")
	type outcome struct {
		res *Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := cb.Wait(context.Background())
		ch <- outcome{res, err}
	}()
	waitUntilWaiting(t, cb)

	code, _ := get(t, cb.URL()+"?error=access_denied&state=wrong")
	if code != http.StatusBadRequest {
		t.Fatalf("статус = %d, want 400", code)
	}
	select {
	case o := <-ch:
		t.Fatalf("чужой error= снял ожидание: %+v", o)
	case <-time.After(100 * time.Millisecond):
	}

	if code, _ := get(t, cb.URL()+"?code=abc&state=st-1"); code != http.StatusOK {
		t.Fatalf("верный редирект: статус = %d, want 200", code)
	}
	select {
	case o := <-ch:
		if o.err != nil || o.res.Code != "abc" {
			t.Fatalf("Wait: %+v", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait не завершился")
	}
}

func TestCallbackEmptyStateRejected(t *testing.T) {
	cb := newTestCallback(t, "st-1")
	type outcome struct {
		res *Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := cb.Wait(context.Background())
		ch <- outcome{res, err}
	}()
	waitUntilWaiting(t, cb)

	if code, _ := get(t, cb.URL()+"?code=abc"); code != http.StatusBadRequest {
		t.Fatalf("статус = %d, want 400", code)
	}
	select {
	case o := <-ch:
		t.Fatalf("редирект без state снял ожидание: %+v", o)
	case <-time.After(100 * time.Millisecond):
	}
	if code, _ := get(t, cb.URL()+"?code=abc&state=st-1"); code != http.StatusOK {
		t.Fatalf("верный редирект: статус = %d, want 200", code)
	}
	if o := <-ch; o.err != nil || o.res.Code != "abc" {
		t.Fatalf("Wait: %+v", o)
	}
}

func TestCallbackAuthorizationError(t *testing.T) {
	cb := newTestCallback(t, "st-1")
	ch := make(chan error, 1)
	go func() {
		_, err := cb.Wait(context.Background())
		ch <- err
	}()
	waitUntilWaiting(t, cb)

	code, _ := get(t, cb.URL()+"?error=access_denied&error_description=user+denied&state=st-1")
	if code != http.StatusBadRequest {
		t.Fatalf("статус = %d, want 400", code)
	}
	select {
	case err := <-ch:
		if err == nil || !strings.Contains(err.Error(), "access_denied") || !strings.Contains(err.Error(), "user denied") {
			t.Fatalf("Wait err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait не завершился")
	}
}

func TestCallbackWaitContextDone(t *testing.T) {
	cb := newTestCallback(t, "st-1")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := cb.Wait(ctx)
	if err == nil {
		t.Fatal("ожидалась ошибка по ctx")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Wait не снялся по ctx за %v", time.Since(start))
	}
}

func TestCallbackSecondWaitRejected(t *testing.T) {
	cb := newTestCallback(t, "st-1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		cb.Wait(ctx)
		close(done)
	}()
	waitUntilWaiting(t, cb)

	start := time.Now()
	_, err := cb.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another authorization is already in progress") {
		t.Fatalf("второй Wait: err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("второй Wait заблокировался")
	}
	cancel()
	<-done
}

func TestCallbackCloseUnblocksWait(t *testing.T) {
	cb := newTestCallback(t, "st-1")
	ch := make(chan error, 1)
	go func() {
		_, err := cb.Wait(context.Background())
		ch <- err
	}()
	waitUntilWaiting(t, cb)
	if err := cb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("Wait после Close вернул успех")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait не снялся по Close")
	}
}

func TestNewCallbackRejectsNonLoopback(t *testing.T) {
	if _, err := NewCallback("0.0.0.0:0", "st-1"); err == nil {
		t.Fatal("ожидалась ошибка на не-loopback адрес")
	}
}

// Пустой ожидаемый state означал бы «принимай любой редирект»; приёмник
// обязан отказаться слушать ещё до открытия порта.
func TestNewCallbackRejectsEmptyState(t *testing.T) {
	if _, err := NewCallback("127.0.0.1:0", ""); err == nil {
		t.Fatal("ожидалась ошибка на пустой ожидаемый state")
	}
}

func waitUntilWaiting(t *testing.T, cb *Callback) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cb.mu.Lock()
		w := cb.waiting
		cb.mu.Unlock()
		if w {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Wait не зарегистрировался")
}
