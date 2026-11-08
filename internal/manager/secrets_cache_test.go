package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeBao — OpenBao KV v2 на httptest: считает GET, чтобы видеть, ходит ли
// Secrets в сеть повторно.
type fakeBao struct {
	mu     sync.Mutex
	values map[string]string
	gets   int
}

func (f *fakeBao) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/secret/")
	var key string
	switch {
	case strings.HasPrefix(name, "data/"):
		key = strings.TrimPrefix(name, "data/mcp/")
	case strings.HasPrefix(name, "metadata/"):
		key = strings.TrimPrefix(name, "metadata/mcp/")
	default:
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		f.gets++
		v, ok := f.values[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]string{"value": v}}})
	case http.MethodPost:
		var body struct {
			Data map[string]string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.values[key] = body.Data["value"]
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"version": "1"}})
	case http.MethodDelete:
		if _, ok := f.values[key]; !ok {
			http.NotFound(w, r)
			return
		}
		delete(f.values, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (f *fakeBao) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

func fakeSecrets(t *testing.T, fake *fakeBao, files map[string]string) *Secrets {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	local, err := loadEnvFile(filepath.Join(t.TempDir(), "env.json"))
	if err != nil {
		t.Fatalf("env.json: %v", err)
	}
	for k, v := range files {
		if err := local.set(k, v); err != nil {
			t.Fatalf("env.json %s: %v", k, err)
		}
	}
	return &Secrets{
		local: local,
		bao:   &bao{addr: srv.URL, token: "t", mount: "secret", prefix: "mcp", client: newBaoClient()},
	}
}

// Второй Get не идёт в сеть, Set сбрасывает запись.
func TestSecretsGetCachesBao(t *testing.T) {
	fake := &fakeBao{values: map[string]string{"TOKEN": "v1"}}
	sec := fakeSecrets(t, fake, nil)

	if v, source := sec.Get("TOKEN"); v != "v1" || source != sourceOpenBao {
		t.Fatalf("Get = %q из %q", v, source)
	}
	sec.Get("TOKEN")
	if n := fake.getCount(); n != 1 {
		t.Fatalf("второй Get сходил в OpenBao: запросов %d, ожидался 1", n)
	}

	if _, err := sec.Set("TOKEN", "v2", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, _ := sec.Get("TOKEN"); v != "v2" {
		t.Fatalf("Set не сбросил кэш: Get = %q", v)
	}
	if n := fake.getCount(); n != 2 {
		t.Fatalf("после Set Get не обновил значение: запросов %d, ожидалось 2", n)
	}
}

// Секрет, продублированный в env.json и OpenBao, удаляется отовсюду: иначе
// файл снова отдаёт его через Get.
func TestDeleteWithoutStoreRemovesDuplicate(t *testing.T) {
	fake := &fakeBao{values: map[string]string{"FOO": "remote"}}
	sec := fakeSecrets(t, fake, map[string]string{"FOO": "file"})

	if v, source := sec.Get("FOO"); v != "remote" || source != sourceOpenBao {
		t.Fatalf("до удаления Get = %q из %q", v, source)
	}
	removed, source, err := sec.DeleteFrom("FOO", "")
	if err != nil || !removed || source != sourceOpenBao {
		t.Fatalf("DeleteFrom = %v, %q, %v", removed, source, err)
	}
	if v, source := sec.Get("FOO"); v != "" || source != "" {
		t.Fatalf("секрет вернулся: %q из %q", v, source)
	}
	if _, ok := sec.local.get("FOO"); ok {
		t.Fatal("копия осталась в env.json")
	}
}
