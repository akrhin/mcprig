package manager

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSetLogsTailFromFile: логи берутся из файла набора, а не из кольца в
// памяти (после рестарта панели кольцо пустое, файл — нет).
func TestSetLogsTailFromFile(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "logs", "fake")
	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	defer e.m.rt.Stop(set)

	// Кольцо чистим: строка обязана прийти из файла.
	r := e.m.rt.ring(id)
	r.mu.Lock()
	r.lines, r.partial = nil, ""
	r.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for {
		w := e.do(t, http.MethodGet, "/api/sets/"+id+"/logs?lines=5", nil)
		status(t, w, http.StatusOK)
		if strings.Contains(w.Body.String(), "fakeset слушает") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("в логах нет хвоста файла: %s", w.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
