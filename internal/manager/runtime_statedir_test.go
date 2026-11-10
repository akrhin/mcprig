package manager

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/akrhin/mcprig/internal/oauth"
	"github.com/akrhin/mcprig/internal/registry"
)

// Каждый набор получает свой каталог состояния: общий oauth.json затирался
// снимком токенов другого набора.
func TestSetStateDirPerSet(t *testing.T) {
	dataDir := t.TempDir()
	rt := NewRuntime(dataDir, fakeBin, "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var dirs []string
	for _, id := range []string{"alpha", "beta"} {
		set := &registry.Set{ID: id, Port: freePort(t)}
		body := fmt.Sprintf(`{"transport":{"host":"127.0.0.1","port":%d}}`, set.Port)
		cfgPath, err := WriteSetConfig(dataDir, id, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(fakeBin, "serve", "--config", cfgPath)
		cmd.Env = rt.setEnv(set, nil)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

		want := filepath.Join(dataDir, "sets", id)
		if got := waitEnv(t, filepath.Join(want, "child-env")); got != want {
			t.Fatalf("MCPRIG_DATA_DIR набора %s = %q, want %q", id, got, want)
		}
		dirs = append(dirs, want)
	}
	if dirs[0] == dirs[1] {
		t.Fatal("каталог состояния общий у двух наборов")
	}

	for i, dir := range dirs {
		store, err := oauth.OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(oauth.Key("fs", "https://"+dirs[i]+"/mcp"),
			&oauth2.Token{AccessToken: dirs[i], RefreshToken: "refresh"}); err != nil {
			t.Fatal(err)
		}
	}
	// «Перезапуск» — свежий Store на том же каталоге: записи обе на месте.
	for _, dir := range dirs {
		store, err := oauth.OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := store.Load(oauth.Key("fs", "https://"+dir+"/mcp"))
		if !ok || got.AccessToken != dir {
			t.Fatalf("каталог %s: запись не пережила переоткрытие: ok=%v tok=%+v", dir, ok, got)
		}
	}
}

// waitEnv ждёт отметку, которую фейковый набор пишет до начала прослушивания.
func waitEnv(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(raw))
		}
		if time.Now().After(deadline) {
			t.Fatalf("набор не оставил отметку %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
