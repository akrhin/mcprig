package oauth

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

const (
	testServer   = "fs"
	testServerUL = "https://example.com/mcp"
	testRedirect = "http://127.0.0.1:8947/callback"
)

func TestNewHandlerWithoutSavedToken(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(context.Background(), Options{
		ServerName:   testServer,
		ServerURL:    testServerUL,
		RedirectURL:  testRedirect,
		CallbackAddr: "127.0.0.1:0",
		Store:        store,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	ts, err := h.TokenSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ts != nil {
		t.Fatal("без сохранённого токена TokenSource должен быть nil")
	}
}

func TestNewHandlerWithSavedToken(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved := &oauth2.Token{AccessToken: "saved-access", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}
	if err := store.Save(Key(testServer, testServerUL), saved); err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(context.Background(), Options{
		ServerName:  testServer,
		ServerURL:   testServerUL,
		RedirectURL: testRedirect,
		Store:       store,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	ts, err := h.TokenSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ts == nil {
		t.Fatal("с сохранённым токеном TokenSource не должен быть nil")
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != saved.AccessToken {
		t.Fatalf("AccessToken = %q, want %q", tok.AccessToken, saved.AccessToken)
	}
}

func TestNewHandlerPreregisteredClient(t *testing.T) {
	if _, err := NewHandler(context.Background(), Options{
		ServerName:   testServer,
		ServerURL:    testServerUL,
		RedirectURL:  testRedirect,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}); err != nil {
		t.Fatalf("NewHandler с предрегистрацией: %v", err)
	}
}

func TestNewHandlerRequiresRedirectURL(t *testing.T) {
	_, err := NewHandler(context.Background(), Options{ServerName: testServer, ServerURL: testServerUL})
	if err == nil || !strings.Contains(err.Error(), "RedirectURL") {
		t.Fatalf("err = %v", err)
	}
}

// Адрес приёмника берётся из redirect_uri: другой порт означал бы, что браузер
// стучится мимо, и вход висел бы до пятиминутного таймаута.
func TestCallbackAddrFollowsRedirectURL(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	got, err := callbackAddr("http://127.0.0.1:9107/callback", log)
	if err != nil || got != "127.0.0.1:9107" {
		t.Fatalf("callbackAddr = %q, %v", got, err)
	}
	// Без порта — эфемерный, с предупреждением в лог.
	got, err = callbackAddr("http://127.0.0.1/callback", log)
	if err != nil || got != "127.0.0.1:0" {
		t.Fatalf("callbackAddr без порта = %q, %v", got, err)
	}
	if _, err := callbackAddr("://bad", log); err == nil {
		t.Fatal("неверный RedirectURL принят")
	}
}
