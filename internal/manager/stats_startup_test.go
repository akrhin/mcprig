package manager

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Набор, остановленный навсегда, своим Rotate уже не займётся: панель
// сворачивает такие дни при старте, иначе архив теряет их навсегда.
func TestStartupRotatesStatsOfStoppedSet(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "old", "fake")

	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -40)
	dir := filepath.Join(e.dir, "sets", id, "stats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dayName := day.Format("2006-01-02")
	body := fmt.Sprintf(`{"day":%q,"triples":[{"client":"c","server":"s","tool":"t","calls":2,"totalMs":10}]}`, dayName)
	if err := os.WriteFile(filepath.Join(dir, "day-"+dayName+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	m2, err := New(Options{
		DataDir: e.dir,
		Version: "test",
		CLIPath: fakeBin,
		Auth:    AuthOptions{Off: true, AllowedHosts: []string{"example.com"}},
		Log:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m2.Close)

	if _, err := os.Stat(filepath.Join(dir, "month-"+day.Format("2006-01")+".json")); err != nil {
		t.Fatalf("месячный файл не собран при старте: %v", err)
	}
	w := (&env{m: m2, dir: e.dir}).do(t, http.MethodGet, "/api/stats?set="+id+"&period=archive", nil)
	status(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"calls":2`) {
		t.Fatalf("архив не увидел несвёрнутый день: %s", w.Body.String())
	}
}
