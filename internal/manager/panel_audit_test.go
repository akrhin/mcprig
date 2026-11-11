package manager

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
)

// startLocked читает поля процесса под rt.mu: без этого -race ловит гонку с
// wait/kill, которые пишут те же поля под замком.
func TestStartReadsProcStateUnderLock(t *testing.T) {
	rt := NewRuntime(t.TempDir(), "/bin/true", "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	set := &registry.Set{ID: "s", Port: 9100} // без активного ключа: дальше старта не идёт

	done := make(chan struct{})
	rt.mu.Lock()
	rt.procs["s"] = &process{stopping: true, done: done}
	rt.mu.Unlock()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			rt.mu.Lock()
			if p := rt.procs["s"]; p != nil {
				p.exited = !p.exited
				p.stopping = !p.stopping
				p.restarts++
			}
			rt.mu.Unlock()
		}
	}()
	for i := 0; i < 200; i++ {
		_, _ = rt.Start(context.Background(), set, nil)
	}
	close(stop)
	wg.Wait()

	rt.mu.Lock()
	delete(rt.procs, "s")
	rt.mu.Unlock()
}

// tailFile читает хвост, а не весь файл: строки после 5 МиБ на месте.
func TestTailFileReturnsLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "set.log")
	var b strings.Builder
	for i := 0; i < 200000; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", 20))
		b.WriteString("\n")
	}
	b.WriteString("last-1\nlast-2\nlast-3\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := tailFile(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"last-1", "last-2", "last-3"}
	if len(got) != len(want) || got[0] != want[0] || got[2] != want[2] {
		t.Fatalf("хвост = %q", got)
	}
}

// Сбой ротации не выбрасывает вывод: старый файл остаётся, ошибка видна в
// статусе набора.
func TestLogWriterKeepsFileOnRotateFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcprig.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := &ring{}
	w := &logWriter{file: f, path: path, ring: r}
	// Каталог на месте .log.1: rename не пройдёт.
	if err := os.Mkdir(path+".1", 0o700); err != nil {
		t.Fatal(err)
	}

	chunk := []byte(strings.Repeat("x", 1<<20) + "\n")
	for i := 0; i < 6; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("запись %d: %v", i, err)
		}
	}
	if r.lastError() == "" {
		t.Fatal("ошибка ротации не запомнена")
	}
	if w.file == nil {
		t.Fatal("дескриптор лога потерян")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(6*len(chunk)) {
		t.Fatalf("вывод после сбоя ротации потерян: %d байт", info.Size())
	}
}

// Правка работающего набора на занятый порт — 409, набор продолжает работать.
func TestSetUpdateBusyPortKeepsSetRunning(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "s", "a")
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil), http.StatusOK)

	ln, err := net.Listen("tcp", "127.0.0.1:9151")
	if err != nil {
		t.Fatalf("занять порт 9151: %v", err)
	}
	defer ln.Close()

	w := e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{"port": 9151})
	status(t, w, http.StatusConflict)
	if !e.m.rt.status(e.m.store.Set(id)).Running {
		t.Fatal("набор остановлен отбитой правкой")
	}
}

// gc сессий вызывается тикером здоровья и убирает истёкшие.
func TestSessionStoreGCRemovesExpired(t *testing.T) {
	s, err := newSessionStore()
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.items["old"] = session{user: "u", expires: time.Now().Add(-time.Minute)}
	s.items["new"] = session{user: "u", expires: time.Now().Add(time.Hour)}
	s.mu.Unlock()

	s.gc()

	s.mu.Lock()
	_, old := s.items["old"]
	_, fresh := s.items["new"]
	s.mu.Unlock()
	if old {
		t.Fatal("истёкшая сессия не убрана")
	}
	if !fresh {
		t.Fatal("живая сессия убрана")
	}
}

// Сессию просмотра в работе close не закрывает: рефкаунт держит её до release.
func TestBrowseCloseWaitsForRefs(t *testing.T) {
	b := newBrowser(nil, "test")
	b.items["x"] = &browseSession{view: &browseView{ID: "x"}, used: time.Now()}
	s, err := b.lookup("x")
	if err != nil {
		t.Fatal(err)
	}
	if s.refs != 1 {
		t.Fatalf("refs = %d", s.refs)
	}
	if !b.close("x") {
		t.Fatal("close не нашёл сессию")
	}
	if !s.closing {
		t.Fatal("сессия не помечена закрываемой")
	}
	if _, ok := b.items["x"]; ok {
		t.Fatal("сессия осталась в карте")
	}
	// Пока refs не отпущены, sweep её не трогает.
	b.items["y"] = &browseSession{view: &browseView{ID: "y"}, used: time.Now().Add(-2 * browseTTL), refs: 1}
	b.sweep()
	if _, ok := b.items["y"]; !ok {
		t.Fatal("sweep закрыл сессию в работе")
	}
}

// Конфиг набора пишется атомарно и только владельцу: в servers.json лежит ключ.
func TestSetConfigIsOwnerOnly(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "s", "a")
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil), http.StatusOK)

	info, err := os.Stat(filepath.Join(e.dir, "sets", id, "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("servers.json = %o, ожидалось 600", perm)
	}
}

// Отзыв активного ключа останавливает набор и говорит об этом в ответе.
func TestRevokeActiveKeyStopsSet(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "s", "a")
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil), http.StatusOK)

	view := decode[struct {
		Keys []struct {
			ID        string `json:"id"`
			RevokedAt string `json:"revokedAt"`
		} `json:"keys"`
	}](t, e.do(t, http.MethodGet, "/api/sets/"+id, nil))
	keyID := ""
	for _, k := range view.Keys {
		if k.RevokedAt == "" {
			keyID = k.ID
		}
	}
	if keyID == "" {
		// Набор без ключей (auth выключен): выдаём ключ — он станет активным.
		issued := decode[struct {
			Key struct {
				ID string `json:"id"`
			} `json:"key"`
		}](t, e.do(t, http.MethodPost, "/api/sets/"+id+"/keys", nil))
		keyID = issued.Key.ID
	}
	if keyID == "" {
		t.Fatal("активного ключа нет")
	}

	w := e.do(t, http.MethodDelete, "/api/sets/"+id+"/keys/"+keyID, nil)
	status(t, w, http.StatusOK)
	got := decode[struct {
		Stopped bool `json:"stopped"`
	}](t, w)
	if !got.Stopped {
		t.Fatal("ответ не сказал об остановке набора")
	}
	if e.m.rt.status(e.m.store.Set(id)).Running {
		t.Fatal("набор работает без активного ключа")
	}
}
