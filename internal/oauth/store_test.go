package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestKeyDeterministicAndHashed(t *testing.T) {
	k := Key("fs", "https://example.com/mcp")
	if len(k) != 32 {
		t.Fatalf("len(Key) = %d, want 32", len(k))
	}
	sum := sha256.Sum256([]byte("fs\x00https://example.com/mcp"))
	want := hex.EncodeToString(sum[:])[:32]
	if k != want {
		t.Fatalf("Key = %q, want %q", k, want)
	}
	if Key("fs", "https://example.com/mcp") != k {
		t.Fatal("Key не детерминирован")
	}
	if Key("other", "https://example.com/mcp") == k || Key("fs", "https://other/mcp") == k {
		t.Fatal("Key не зависит от имени и URL")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	tok := &oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour).Round(time.Second),
	}
	key := Key("fs", "https://example.com/mcp")
	if err := s.Save(key, tok); err != nil {
		t.Fatal(err)
	}

	got, ok := s.Load(key)
	if !ok {
		t.Fatal("Load не нашёл сохранённый токен")
	}
	if got.AccessToken != tok.AccessToken || got.RefreshToken != tok.RefreshToken ||
		got.TokenType != tok.TokenType || !got.Expiry.Equal(tok.Expiry) {
		t.Fatalf("Load = %+v, want %+v", got, tok)
	}

	info, err := os.Stat(filepath.Join(dir, "oauth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("права файла = %o, want 600", perm)
	}

	// Новый Store видит записанное.
	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Load(key); !ok {
		t.Fatal("после переоткрытия токен потерян")
	}

	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Load(key); ok {
		t.Fatal("Delete не удалил токен")
	}
	s3, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s3.Load(key); ok {
		t.Fatal("удаление не сохранилось на диск")
	}
}

func TestStoreConcurrentSave(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				key := Key("server", string(rune('a'+i))+string(rune('0'+j)))
				if err := s.Save(key, &oauth2.Token{AccessToken: key}); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	s2, err := OpenStore(s.dir)
	if err != nil {
		t.Fatalf("файл повреждён конкурентной записью: %v", err)
	}
	data, err := os.ReadFile(s2.path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]*oauth2.Token
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("невалидный JSON после конкурентных Save: %v", err)
	}
	if len(m) != 80 {
		t.Fatalf("записано %d токенов, want 80", len(m))
	}
}

func TestOpenStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oauth.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenStore(dir)
	if err == nil {
		t.Fatal("ожидалась ошибка на битом файле")
	}
	if !contains(err.Error(), path) {
		t.Fatalf("ошибка без пути к файлу: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
