package oauth

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
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

// Занятый порт из redirect_uri обязан сорвать приём кода сразу — иначе вход
// висит пять минут. Здесь же ловится мутация «вернуть DefaultCallbackAddr»:
// тогда приёмник ушёл бы на свободный 8947 и ждал до таймаута.
func TestNewHandlerBindsRedirectPort(t *testing.T) {
	saved := BrowserOpen
	BrowserOpen = func(string) error { return nil }
	t.Cleanup(func() { BrowserOpen = saved })

	newHandler := func(redirect string) *Handler {
		t.Helper()
		h, err := NewHandler(context.Background(), Options{
			ServerName:  testServer,
			ServerURL:   testServerUL,
			RedirectURL: redirect,
		})
		if err != nil {
			t.Fatalf("NewHandler(%s): %v", redirect, err)
		}
		return h
	}

	// P занят: приём кода обязан упасть на нём, а не молча слушать 8947.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	_, port, err := net.SplitHostPort(busy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	h := newHandler("http://127.0.0.1:" + port + "/callback")
	if got, want := h.Addr(), "127.0.0.1:"+port; got != want {
		t.Fatalf("Addr = %q, want %q", got, want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = h.fetch(ctx, &auth.AuthorizationArgs{URL: "https://as.example/authorize?state=st-1"})
	if err == nil || !strings.Contains(err.Error(), "занять порт") {
		t.Fatalf("занятый порт: err = %v", err)
	}

	// Свободный порт P2 — адрес приёмника тот же, что в redirect_uri.
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port2, err := net.SplitHostPort(ln2.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ln2.Close()
	if got, want := newHandler("http://127.0.0.1:"+port2+"/callback").Addr(), "127.0.0.1:"+port2; got != want {
		t.Fatalf("Addr = %q, want %q", got, want)
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
