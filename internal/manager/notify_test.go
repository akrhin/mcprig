package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
)

// hook — приёмник вебхука: копит тела и разобранные события.
type hook struct {
	srv    *httptest.Server
	delay  time.Duration
	mu     sync.Mutex
	bodies []string
	events []notifyEvent
}

func newHook(t *testing.T, delay time.Duration) *hook {
	t.Helper()
	h := &hook{delay: delay}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev notifyEvent
		_ = json.Unmarshal(body, &ev)
		time.Sleep(h.delay)
		h.mu.Lock()
		h.bodies = append(h.bodies, string(body))
		h.events = append(h.events, ev)
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hook) got() ([]notifyEvent, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]notifyEvent(nil), h.events...), append([]string(nil), h.bodies...)
}

// want ждёт ровно n событий и возвращает тела.
func (h *hook) want(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if events, bodies := h.got(); len(events) == n {
			return bodies
		}
		time.Sleep(5 * time.Millisecond)
	}
	events, _ := h.got()
	t.Fatalf("событий: %d, ожидалось %d (%+v)", len(events), n, events)
	return nil
}

// crashWait роняет «процесс» набора так же, как настоящий супервизор: wait
// получает вышедший процесс. Перезапуск отключён — rt.reload отдаёт nil.
func crashWait(t *testing.T, rt *Runtime, set *registry.Set, code, restarts int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("exit %d", code))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "log-*")
	if err != nil {
		t.Fatal(err)
	}
	p := &process{cmd: cmd, pid: cmd.Process.Pid, restarts: restarts, done: make(chan struct{})}
	rt.mu.Lock()
	rt.procs[set.ID] = p
	rt.mu.Unlock()
	rt.wait(set.ID, set, nil, p, logFile)
}

func quietRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime(t.TempDir(), "/bin/true", "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rt.reload = func(string) (*registry.Set, []registry.Server) { return nil, nil }
	t.Cleanup(rt.notify.drain)
	return rt
}

// Один переход — одно событие: набор, который так и не поднялся, не шлёт
// второе уведомление о втором падении.
func TestNotifyOnCrashOncePerTransition(t *testing.T) {
	h := newHook(t, 0)
	rt := quietRuntime(t)
	set := &registry.Set{ID: "crash", Notify: &registry.Notify{Webhook: h.srv.URL}}

	crashWait(t, rt, set, 3, 0)
	crashWait(t, rt, set, 3, 0)
	bodies := h.want(t, 1)
	time.Sleep(100 * time.Millisecond)
	if events, _ := h.got(); len(events) != 1 {
		t.Fatalf("два падения дали %d событий", len(events))
	}

	var ev notifyEvent
	if err := json.Unmarshal([]byte(bodies[0]), &ev); err != nil {
		t.Fatalf("тело: %v", err)
	}
	if ev.Event != "set.crashed" || ev.Set != "crash" || ev.Code != 3 || ev.At == "" {
		t.Fatalf("событие: %+v", ev)
	}
}

// Исчерпанный бюджет перезапусков — своё событие, и только оно.
func TestNotifyRestartFailed(t *testing.T) {
	h := newHook(t, 0)
	rt := quietRuntime(t)
	set := &registry.Set{ID: "dead", Notify: &registry.Notify{Webhook: h.srv.URL}}

	crashWait(t, rt, set, 7, maxRestarts)
	h.want(t, 1)
	if events, _ := h.got(); events[0].Event != "set.restart_failed" || events[0].Code != 7 {
		t.Fatalf("событие: %+v", events[0])
	}
}

// Тело уходит на чужой хост: в нём нет ни токена набора, ни env — только
// пять полей события.
func TestNotifyNoSecretLeak(t *testing.T) {
	h := newHook(t, 0)
	rt := quietRuntime(t)
	set := &registry.Set{
		ID:      "leak",
		Token:   "tok_secret_do_not_leak",
		Servers: []string{"db"},
		Notify:  &registry.Notify{Webhook: h.srv.URL},
	}

	crashWait(t, rt, set, 5, 0)
	body := h.want(t, 1)[0]
	for _, secret := range []string{"tok_secret_do_not_leak", "DB_PASSWORD", "s3cret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("в теле вебхука %q: %s", secret, body)
		}
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("тело: %v", err)
	}
	for k := range fields {
		switch k {
		case "event", "set", "server", "code", "at":
		default:
			t.Fatalf("лишнее поле %q в теле: %s", k, body)
		}
	}
}

// Недоступный вебхук не держит жизненный цикл: ответ идёт дольше, чем
// возвращается обработка падения.
func TestNotifyUnreachableDoesNotBlock(t *testing.T) {
	h := newHook(t, 1500*time.Millisecond)
	rt := quietRuntime(t)
	set := &registry.Set{ID: "slow", Notify: &registry.Notify{Webhook: h.srv.URL}}

	start := time.Now()
	crashWait(t, rt, set, 2, 0)
	if d := time.Since(start); d > 750*time.Millisecond {
		t.Fatalf("обработка падения ждала вебхук: %s", d)
	}
}

// Недоступный адрес: три попытки, ни паники, ни ретраев сверх. Адрес с
// query-токеном в журнал не попадает ни одной строкой.
func TestNotifyDeadWebhookLogsAndStops(t *testing.T) {
	var hlog captureHandler
	rt := NewRuntime(t.TempDir(), "/bin/true", "", nil, slog.New(&hlog))
	rt.reload = func(string) (*registry.Set, []registry.Server) { return nil, nil }
	set := &registry.Set{ID: "down", Notify: &registry.Notify{Webhook: "http://127.0.0.1:1/hook?token=s3cret"}}

	crashWait(t, rt, set, 2, 0)
	rt.notify.drain()

	line, n := hlog.find("вебхук набора не отправлен")
	if n != 1 {
		t.Fatalf("строк про вебхук: %d", n)
	}
	if line.attrs["set"] != "down" {
		t.Fatalf("в строке нет набора: %+v", line.attrs)
	}
	for _, r := range hlog.all() {
		text := r.msg
		for k, v := range r.attrs {
			text += " " + k + "=" + v
		}
		if strings.Contains(text, "s3cret") || strings.Contains(text, "token=") {
			t.Fatalf("адрес вебхука в журнале: %s", text)
		}
	}
}

// Недоставленное событие не считается отправленным: следующее то же событие
// уходит снова, а не подавляется меткой (§C9).
func TestNotifyRetriesAfterFailedDelivery(t *testing.T) {
	h := newHook(t, 0)
	rt := quietRuntime(t)

	rt.notify.emit("http://127.0.0.1:1/hook", "again", "", "set.crashed", 1)
	rt.notify.drain()
	rt.notify.emit(h.srv.URL, "again", "", "set.crashed", 1)
	h.want(t, 1)
}

// Неисправный сервер набора шлёт server.unhealthy один раз на переход, а
// выздоровление открывает следующий.
func TestNotifyUnhealthyServer(t *testing.T) {
	h := newHook(t, 0)
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "web", "fake")
	if _, err := e.m.store.UpdateSet(id, json.RawMessage(`{"notify":{"webhook":"`+h.srv.URL+`"}}`)); err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}

	down, up := false, true
	view := setHealth{Servers: []serverHealth{{Name: "fake", Healthy: &down, Error: "нет соединения"}}}
	e.m.noteHealth(id, view)
	e.m.noteHealth(id, view)
	h.want(t, 1)
	if events, _ := h.got(); events[0].Event != "server.unhealthy" || events[0].Server != "fake" {
		t.Fatalf("событие: %+v", events[0])
	}

	view.Servers[0].Healthy = &up
	e.m.noteHealth(id, view)
	view.Servers[0].Healthy = &down
	e.m.noteHealth(id, view)
	h.want(t, 2)
	e.m.rt.notify.drain()
}

// Удалённый набор не подавляет события нового набора с тем же id: отметка
// «набор/сервер» живёт в памяти и без чистки пережила бы удаление.
func TestNotifyStateClearedOnDelete(t *testing.T) {
	h := newHook(t, 0)
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")

	create := func() string {
		w := e.do(t, http.MethodPost, "/api/sets", map[string]any{
			"id": "dup", "name": "dup", "servers": []string{"fake"},
			"notify": map[string]any{"webhook": h.srv.URL},
		})
		status(t, w, http.StatusCreated)
		return decode[struct {
			ID string `json:"id"`
		}](t, w).ID
	}
	down := false
	view := setHealth{Servers: []serverHealth{{Name: "fake", Healthy: &down, Error: "нет соединения"}}}

	id := create()
	e.m.noteHealth(id, view)
	h.want(t, 1)

	status(t, e.do(t, http.MethodDelete, "/api/sets/"+id, nil), http.StatusOK)
	if same := create(); same != id {
		t.Fatalf("новый набор получил id %q вместо %q", same, id)
	}
	e.m.noteHealth(id, view)
	h.want(t, 2)
	e.m.rt.notify.drain()
}

// Мусор в notify отвергается API с причиной в теле, годный адрес принимается.
func TestNotifyBadWebhookRejected(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")

	w := e.do(t, http.MethodPost, "/api/sets", map[string]any{
		"name": "bad", "servers": []string{"fake"},
		"notify": map[string]any{"webhook": "ftp://example.com/hook"},
	})
	status(t, w, http.StatusBadRequest)

	w = e.do(t, http.MethodPost, "/api/sets", map[string]any{
		"name": "good", "servers": []string{"fake"},
		"notify": map[string]any{"webhook": "https://example.com/hook"},
	})
	status(t, w, http.StatusCreated)
}

// Адрес вебхука проверяется и в самом post: file:// и user:pass@ пришли бы из
// реестра, который правят руками, и запрос ушёл бы мимо вебхука.
func TestNotifyPostRejectsUnsafeWebhook(t *testing.T) {
	n := newNotifier(slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, hook := range []string{
		"file:///etc/passwd",
		"http://user:pass@example.com/hook",
		"ftp://example.com/hook",
		"http:///nohost",
		"://bad",
	} {
		if err := n.post(hook, []byte(`{}`)); err == nil {
			t.Fatalf("адрес %q принят", hook)
		}
	}
}

// all — все строки журнала: проверка «адреса нет ни в одной».
func (h *captureHandler) all() []captured {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]captured(nil), h.recs...)
}

func procOf(t *testing.T, rt *Runtime, id string) *process {
	t.Helper()
	p := rt.proc(id)
	if p == nil {
		t.Fatalf("у набора %s нет процесса", id)
	}
	return p
}

// waitRestart ждёт автоперезапуска: новый процесс, отвечающий порт и снятую
// отметку перехода — её startLocked снимает последней строкой успешного старта.
func waitRestart(t *testing.T, rt *Runtime, id string, old *process, port int) *process {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rt.mu.Lock()
		p := rt.procs[id]
		live := p != nil && p != old && !p.exited
		rt.mu.Unlock()
		if !live {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if !waitPort(context.Background(), rt.probeAddr, port, 10*time.Second, p.done) {
			t.Fatal("набор перезапустился, но порт молчит")
		}
		for time.Now().Before(deadline) {
			rt.notify.mu.Lock()
			marked := rt.notify.last[id+"/"] != ""
			rt.notify.mu.Unlock()
			if !marked {
				return p
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("успешный старт не снял отметку перехода")
	}
	t.Fatal("автоперезапуск не случился")
	return nil
}

// Успешный старт снимает отметку: второй обрыв после перезапуска — новое
// событие. Без rt.notify.clear в startLocked тест молчит.
func TestNotifyAfterSuccessfulRestart(t *testing.T) {
	h := newHook(t, 0)
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "state", "fake")
	if _, err := e.m.store.UpdateSet(id, json.RawMessage(`{"notify":{"webhook":"`+h.srv.URL+`"}}`)); err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}
	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	t.Cleanup(func() { e.m.rt.Stop(set) })

	first := procOf(t, e.m.rt, id)
	if err := syscall.Kill(-first.pid, syscall.SIGKILL); err != nil {
		t.Fatalf("падение: %v", err)
	}
	h.want(t, 1)

	second := waitRestart(t, e.m.rt, id, first, set.Port)
	if err := syscall.Kill(-second.pid, syscall.SIGKILL); err != nil {
		t.Fatalf("второе падение: %v", err)
	}
	h.want(t, 2)
	if events, _ := h.got(); events[1].Event != "set.crashed" || events[1].Set != id {
		t.Fatalf("второе событие: %+v", events[1])
	}
	e.m.rt.notify.drain()
}

// Перезапуск после падения не удался (порт занят) — событие, иначе набор
// остаётся лежать молча.
func TestNotifyRestartFailedAfterCrash(t *testing.T) {
	h := newHook(t, 0)
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "stuck", "fake")
	if _, err := e.m.store.UpdateSet(id, json.RawMessage(`{"notify":{"webhook":"`+h.srv.URL+`"}}`)); err != nil {
		t.Fatalf("UpdateSet: %v", err)
	}
	set := e.m.store.Set(id)
	if _, err := e.m.rt.Start(context.Background(), set, e.m.store.Servers()); err != nil {
		t.Fatalf("старт: %v", err)
	}
	t.Cleanup(func() { e.m.rt.Stop(set) })

	p := procOf(t, e.m.rt, id)
	if err := syscall.Kill(-p.pid, syscall.SIGKILL); err != nil {
		t.Fatalf("падение: %v", err)
	}
	// Чужой слушатель на порту набора: автоперезапуск через 1 с упрётся в него.
	// Свой порт мёртвый процесс отпускает только после выхода — ждём done.
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("упавший процесс не закрылся")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(set.Port)))
	if err != nil {
		t.Fatalf("занять порт: %v", err)
	}
	defer ln.Close()

	h.want(t, 2)
	events, _ := h.got()
	if events[0].Event != "set.crashed" || events[1].Event != "set.restart_failed" || events[1].Set != id {
		t.Fatalf("события: %+v", events)
	}
	e.m.rt.notify.drain()
}
