package manager

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
)

// appliedView — что фейковый набор отдаёт на GET /applied: поколение и список
// серверов, которые он перечитал из servers.json.
type appliedView struct {
	Generation int      `json:"generation"`
	Servers    []string `json:"servers"`
	Probe      string   `json:"probe"`
}

func getApplied(t *testing.T, port int) appliedView {
	t.Helper()
	var last string
	for i := 0; i < 40; i++ {
		res, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/applied")
		if err == nil {
			var v appliedView
			dec := json.NewDecoder(res.Body)
			derr := dec.Decode(&v)
			res.Body.Close()
			if derr == nil {
				return v
			}
			last = "ответ не разобран"
		} else {
			last = err.Error()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("набор не ответил на /applied: %s", last)
	return appliedView{}
}

// childPid читает pid дочернего процесса, который фейковый набор поднял.
func childPid(t *testing.T, dir, id string) int {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "sets", id, "child.pid"))
	if err != nil {
		t.Fatalf("pid дочернего процесса: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		t.Fatalf("pid дочернего процесса: %v", err)
	}
	return pid
}

// alive — процесс жив и не зомби: родитель-фейк его не подбирает, и kill(pid,0)
// на зомби отвечает успехом.
func alive(pid int) bool {
	body, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// Формат: pid (comm) state …; comm в скобках может содержать пробелы.
	i := strings.LastIndexByte(string(body), ')')
	return i >= 0 && i+2 < len(body) && body[i+2] != 'Z'
}

// SIGHUP перезагрузки идёт только лидеру группы: stdio-дети набора его не
// переживают, и правка одного сервера убивала бы соседей. Ребёнок здесь —
// процесс без обработчиков сигналов, как настоящий stdio-сервер; тест падает,
// если сигнал снова уйдёт всей группе.
func TestReloadKeepsStdioChildren(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "first")
	e.addServer(t, "second")
	id := e.addSet(t, "children", "first", "second")
	dir := filepath.Join(e.dir, "sets", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.want"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	started := decode[RuntimeStatus](t, w)
	pid := childPid(t, e.dir, id)
	if !alive(pid) {
		t.Fatalf("дочерний процесс %d не поднялся", pid)
	}

	status(t, e.do(t, http.MethodPut, "/api/servers/second", map[string]any{"description": "правка"}), http.StatusOK)

	// Доставка сигнала асинхронна: держим окно, в котором смерть от SIGHUP
	// успела бы проявиться.
	for i := 0; i < 40; i++ {
		if !alive(pid) {
			t.Fatalf("SIGHUP убил дочерний процесс %d — сигнал ушёл группе, а не лидеру", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := e.m.rt.status(e.m.store.Set(id)); got.PID != started.PID {
		t.Fatalf("pid набора сменился: %d → %d", started.PID, got.PID)
	}
	// Ребёнок жив и reload не упал — но проверяем доставку: процесс набора
	// должен подтвердить новое поколение, иначе тест зелёный и без SIGHUP.
	if applied := getApplied(t, started.Port); applied.Generation < 1 {
		t.Fatalf("reload не дошёл до набора: generation = %d", applied.Generation)
	}
}

// Рестарт начинает поколение заново: старый ack не должен выглядеть ответом
// нового процесса, иначе reload после рестарта «отказывает» без причины.
func TestReloadAfterRestartUsesFreshGeneration(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "fresh", "a")
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil), http.StatusOK)
	for i := 0; i < 3; i++ {
		status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/reload", nil), http.StatusOK)
	}
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/restart", nil), http.StatusOK)

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/reload", nil)
	status(t, w, http.StatusOK)
	out := decode[struct {
		Reloaded   bool `json:"reloaded"`
		Generation int  `json:"generation"`
	}](t, w)
	if !out.Reloaded || out.Generation != 1 {
		t.Fatalf("после рестарта поколение не сброшено: %s", w.Body.String())
	}
}

// Запоздалый ack прежнего процесса (чужая метка, большое поколение) не
// принимается за ответ и не отбивает новую попытку.
func TestReloadIgnoresForeignAck(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "foreign", "a")
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil), http.StatusOK)
	forged := `{"nonce":"0123456789abcdef","generation":50,"applied":["old"],"restartNeeded":[]}`
	if err := os.WriteFile(filepath.Join(e.dir, "sets", id, "reload.status"), []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/reload", nil)
	status(t, w, http.StatusOK)
	out := decode[struct {
		Reloaded   bool `json:"reloaded"`
		Generation int  `json:"generation"`
	}](t, w)
	if !out.Reloaded || out.Generation != 1 {
		t.Fatalf("чужой ack принят за ответ: %s", w.Body.String())
	}
}

// Правка второго сервера не перезапускает набор: тот же pid, бюджет рестартов
// не тронут, первый сервер продолжает отвечать, поколение конфига выросло.
func TestReloadKeepsOtherSessions(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "first")
	e.addServer(t, "second")
	id := e.addSet(t, "live", "first", "second")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	started := decode[RuntimeStatus](t, w)

	// Правка второго сервера через API: набор остаётся тем же процессом.
	w = e.do(t, http.MethodPut, "/api/servers/second", map[string]any{"description": "правка"})
	status(t, w, http.StatusOK)
	edited := decode[struct {
		Reloaded     []string          `json:"reloaded"`
		ReloadErrors map[string]string `json:"reloadErrors"`
	}](t, w)
	if len(edited.Reloaded) != 1 || edited.Reloaded[0] != id {
		t.Fatalf("набор не перезагружен: %s", w.Body.String())
	}
	if len(edited.ReloadErrors) != 0 {
		t.Fatalf("отказы reload: %v", edited.ReloadErrors)
	}

	set := e.m.store.Set(id)
	after := e.m.rt.status(set)
	if after.PID != started.PID {
		t.Fatalf("pid сменился: %d → %d — набор перезапущен, а не перезагружен", started.PID, after.PID)
	}
	if !after.Running {
		t.Fatal("набор не работает после reload")
	}
	if after.Restarts != 0 {
		t.Fatalf("restarts = %d, ожидался 0", after.Restarts)
	}

	// «Живая сессия» здесь — сам процесс набора: он ответил на SIGHUP и отдал
	// оба сервера, то есть старый процесс дожил до ответа и перечитал файл.
	applied := getApplied(t, started.Port)
	if applied.Generation < 1 {
		t.Fatalf("generation = %d, ожидалось ≥ 1", applied.Generation)
	}
	if len(applied.Servers) != 2 || applied.Servers[0] != "first" || applied.Servers[1] != "second" {
		t.Fatalf("применены серверы %v, ожидались first и second", applied.Servers)
	}
}

// Подпись ключа едет через reload: набор не перезапускается (pid тот же), а
// процесс набора подтверждает новое поколение конфига (§C5).
func TestKeyNoteReloadsWithoutRestart(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fake")
	id := e.addSet(t, "signed", "fake")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	started := decode[RuntimeStatus](t, w)

	keys := decode[struct {
		Keys []struct {
			ID string `json:"id"`
		} `json:"keys"`
	}](t, e.do(t, http.MethodGet, "/api/sets/"+id+"/keys", nil)).Keys
	if len(keys) == 0 {
		t.Fatal("у набора нет ключа")
	}

	w = e.do(t, http.MethodPatch, "/api/sets/"+id+"/keys/"+keys[0].ID, map[string]any{"note": "hermes"})
	status(t, w, http.StatusOK)
	if got := decode[struct {
		Key struct {
			Note string `json:"note"`
		} `json:"key"`
	}](t, w).Key.Note; got != "hermes" {
		t.Fatalf("подпись = %q, ожидалась hermes", got)
	}

	after := e.m.rt.status(e.m.store.Set(id))
	if after.PID != started.PID {
		t.Fatalf("подпись ключа перезапустила набор: pid %d → %d", started.PID, after.PID)
	}
	if applied := getApplied(t, started.Port); applied.Generation < 1 {
		t.Fatalf("reload не дошёл до набора: generation = %d", applied.Generation)
	}
}

// Набор не запущен: reload пишет servers.json и отвечает 200 без запуска.
func TestReloadEndpointOnStoppedSet(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "stopped", "a")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/reload", nil)
	status(t, w, http.StatusOK)
	got := decode[struct {
		Reloaded bool   `json:"reloaded"`
		Reason   string `json:"reason"`
	}](t, w)
	if got.Reloaded {
		t.Fatalf("набор не запущен, а ответ reloaded:true: %s", w.Body.String())
	}
	if got.Reason != "набор не запущен" {
		t.Fatalf("reason = %q", got.Reason)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "sets", id, "servers.json")); err != nil {
		t.Fatalf("servers.json не записан: %v", err)
	}
}

// Смена транспорта без перезапуска не применяется: набор поднимается заново.
func TestReloadTransportChangeNeedsRestart(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "transport", "a")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	started := decode[RuntimeStatus](t, w)

	// Открытость теперь применяется живьём (SetAuth слушателя), поэтому
	// перезапуска требует порт — единственное поле, которое процесс читает
	// только при старте.
	w = e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{"port": freePort(t)})
	status(t, w, http.StatusOK)

	after := e.m.rt.status(e.m.store.Set(id))
	if after.PID == started.PID {
		t.Fatal("pid тот же — смена транспорта не перезапустила набор")
	}
	if !after.Running {
		t.Fatalf("набор не поднялся после перезапуска: %+v", after)
	}
}

// Процесс умер до ответа: reload возвращает отказ, супервизор поднимает набор
// как при падении — паники нет и висящего ожидания нет.
func TestReloadFallsBackWhenProcessDies(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "dies", "a")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	started := decode[RuntimeStatus](t, w)

	// Метка: на SIGHUP фейковый набор выходит, не написав reload.status.
	if err := os.WriteFile(filepath.Join(e.dir, "sets", id, "reload.die"), []byte("1"), 0o600); err != nil {
		t.Fatalf("метка: %v", err)
	}
	w = e.do(t, http.MethodPost, "/api/sets/"+id+"/reload", nil)
	status(t, w, http.StatusOK)
	got := decode[struct {
		Reloaded      bool     `json:"reloaded"`
		RestartNeeded []string `json:"restartNeeded"`
		Error         string   `json:"error"`
	}](t, w)
	if got.Reloaded {
		t.Fatalf("процесс умер, а ответ reloaded:true: %s", w.Body.String())
	}
	if !strings.Contains(got.Error, "завершился") {
		t.Fatalf("error = %q", got.Error)
	}
	if len(got.RestartNeeded) != 1 || got.RestartNeeded[0] != "process" {
		t.Fatalf("restartNeeded = %v", got.RestartNeeded)
	}

	// Аварийный перезапуск делает wait, как при падении: набор снова живой.
	set := e.m.store.Set(id)
	deadline := time.Now().Add(readyBudget + 10*time.Second)
	for {
		st := e.m.rt.status(set)
		if st.Running && st.PID != started.PID {
			if st.Restarts < 1 {
				t.Fatalf("restarts = %d, ожидался ≥ 1", st.Restarts)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("набор не поднялся: %+v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Битый или отсутствующий reload.status не роняет ответ: поколение 0, дальше —
// обычная перезагрузка.
func TestReloadStatusParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reload.status")

	if got := readReloadStatus(path); got.Generation != 0 || got.Error != "" {
		t.Fatalf("нет файла: %+v", got)
	}
	if err := os.WriteFile(path, []byte("{это не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readReloadStatus(path); got.Generation != 0 || got.Error != "" {
		t.Fatalf("битый файл: %+v", got)
	}
	if err := os.WriteFile(path, []byte(`{"generation":7,"applied":["a"],"restartNeeded":["port"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readReloadStatus(path); got.Generation != 7 || len(got.RestartNeeded) != 1 {
		t.Fatalf("годный файл: %+v", got)
	}

	// Битый файл от прошлой попытки не мешает живой перезагрузке.
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "garbage", "a")
	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	if err := os.WriteFile(filepath.Join(e.dir, "sets", id, "reload.status"), []byte("мусор"), 0o600); err != nil {
		t.Fatal(err)
	}
	w = e.do(t, http.MethodPost, "/api/sets/"+id+"/reload", nil)
	status(t, w, http.StatusOK)
	out := decode[struct {
		Reloaded   bool `json:"reloaded"`
		Generation int  `json:"generation"`
	}](t, w)
	if !out.Reloaded || out.Generation < 1 {
		t.Fatalf("reload после битого файла: %s", w.Body.String())
	}
}

// Правка набора не теряется, если процесс не применил её: ответ 200 с
// reloadError, а не 500 — по образцу restartError при выдаче ключа.
func TestSetUpdateKeepsEditWhenReloadFails(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "a")
	id := e.addSet(t, "edit", "a")
	status(t, e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil), http.StatusOK)

	if err := os.WriteFile(filepath.Join(e.dir, "sets", id, "reload.die"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := e.do(t, http.MethodPut, "/api/sets/"+id, map[string]any{"description": "новая"})
	status(t, w, http.StatusOK)
	got := decode[struct {
		Description string `json:"description"`
		ReloadError string `json:"reloadError"`
	}](t, w)
	if got.Description != "новая" {
		t.Fatalf("правка потеряна: %s", w.Body.String())
	}
	if !strings.Contains(got.ReloadError, "завершился") {
		t.Fatalf("reloadError = %q", got.ReloadError)
	}
}

// needsRestart — развилка «перезапуск или reload» по полям набора.
func TestNeedsRestart(t *testing.T) {
	base := &registry.Set{ID: "s", Port: 9101, Token: "t", Open: false}
	none := []registry.Server{}
	if restart, _ := needsRestart(base, &registry.Set{ID: "s", Port: 9101, Token: "t"}, none); restart {
		t.Fatal("неизменный транспорт потребовал перезапуска")
	}
	if restart, why := needsRestart(base, &registry.Set{ID: "s", Port: 9102, Token: "t"}, none); !restart || why != "порт" {
		t.Fatalf("порт: %v %q", restart, why)
	}
	// Смена ключа больше не требует перезапуска: список токенов доезжает
	// reload'ом, поэтому решение о перезапуске его не трогает.
	if restart, why := needsRestart(base, &registry.Set{ID: "s", Port: 9101, Token: "u"}, none); restart {
		t.Fatalf("ключ: %v %q", restart, why)
	}
	// Открытость перезапуска не требует: слушатель меняет её живьём
	// (SetAuth), как и список ключей.
	if restart, why := needsRestart(base, &registry.Set{ID: "s", Port: 9101, Token: "t", Open: true}, none); restart {
		t.Fatalf("открытость: %v %q", restart, why)
	}
}

// Сервер с новой ${VAR} в составе набора: окружение процесса зафиксировано при
// старте, поэтому без перезапуска переменная осталась бы пустой.
func TestNeedsRestartOnNewEnvRef(t *testing.T) {
	servers := []registry.Server{
		{Name: "plain", Transport: "stdio", Command: []string{"/bin/true"}},
		{Name: "wants", Transport: "stdio", Command: []string{"/bin/true"}, Env: map[string]string{"TOKEN": "${NEW}"}},
	}
	old := &registry.Set{ID: "s", Port: 9101, Servers: []string{"plain"}}
	if restart, why := needsRestart(old, &registry.Set{ID: "s", Port: 9101, Servers: []string{"plain", "wants"}}, servers); !restart || !strings.Contains(why, "NEW") {
		t.Fatalf("новая переменная: %v %q", restart, why)
	}
	// Тот же сервер без ссылок на переменные перезапуска не требует.
	if restart, why := needsRestart(old, &registry.Set{ID: "s", Port: 9101, Servers: []string{"plain"}}, servers); restart {
		t.Fatalf("без новых переменных: %v %q", restart, why)
	}
}

// Правка окружения сервера не доезжает до запущенного набора без перезапуска:
// envAllow становится окружением процесса набора при старте, а ${VAR} сервера
// раскрывается из уже запущенного процесса. Поэтому панель перезапускает наборы
// с этим сервером, а не шлёт им reload.
func TestServerEnvChangeRestartsSet(t *testing.T) {
	t.Setenv("RELOAD_PROBE", "one")
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "first")
	id := e.addSet(t, "live", "first")

	w := e.do(t, http.MethodPost, "/api/sets/"+id+"/start", nil)
	status(t, w, http.StatusOK)
	started := decode[RuntimeStatus](t, w)
	if got := getApplied(t, started.Port).Probe; got != "" {
		t.Fatalf("RELOAD_PROBE видна до правки: %q", got)
	}

	w = e.do(t, http.MethodPut, "/api/servers/first", map[string]any{
		"envAllow": []string{"RELOAD_PROBE"},
	})
	status(t, w, http.StatusOK)
	edited := decode[struct {
		Reloaded     []string          `json:"reloaded"`
		ReloadErrors map[string]string `json:"reloadErrors"`
	}](t, w)
	if len(edited.Reloaded) != 1 || edited.Reloaded[0] != id {
		t.Fatalf("набор не перезапущен после правки envAllow: %s", w.Body.String())
	}
	if len(edited.ReloadErrors) != 0 {
		t.Fatalf("отказы reload: %v", edited.ReloadErrors)
	}

	set := e.m.store.Set(id)
	after := e.m.rt.status(set)
	if after.PID == started.PID {
		t.Fatalf("pid не сменился (%d): набор перезагружен, а не перезапущен", after.PID)
	}
	if !after.Running {
		t.Fatal("набор не работает после перезапуска")
	}
	if got := getApplied(t, after.Port).Probe; got != "one" {
		t.Fatalf("новый процесс набора не видит RELOAD_PROBE: %q", got)
	}

	// Обратная сторона: правка без окружения остаётся reload'ом.
	w = e.do(t, http.MethodPut, "/api/servers/first", map[string]any{"description": "правка"})
	status(t, w, http.StatusOK)
	again := e.m.rt.status(set)
	if again.PID != after.PID {
		t.Fatalf("правка description перезапустила набор: pid %d → %d", after.PID, again.PID)
	}
	if got := getApplied(t, after.Port).Generation; got < 1 {
		t.Fatalf("generation = %d после reload", got)
	}
}
