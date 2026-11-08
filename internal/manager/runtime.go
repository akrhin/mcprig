package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/registry"
)

const (
	readyBudget = 15 * time.Second
	readyPoll   = 250 * time.Millisecond
	maxRestarts = 5
	maxBackoff  = 30 * time.Second
	stopGrace   = 5 * time.Second
	closeGrace  = 3 * time.Second
	maxLogBytes = 5 << 20
	ringLines   = 2000
	partialCap  = 64 << 10
)

// RuntimeStatus — то, что отдаётся клиенту по start/stop/restart.
type RuntimeStatus struct {
	ID        string `json:"id"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
	Port      int    `json:"port"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Restarts  int    `json:"restarts,omitempty"`
	Error     string `json:"error,omitempty"`
}

type process struct {
	cmd       *exec.Cmd
	pid       int
	port      int
	startedAt time.Time
	restarts  int
	stopping  bool
	exited    bool
	exitCode  int
	err       string
	done      chan struct{}
}

// Runtime — супервизия наборов: один процесс на набор, своя блокировка на
// набор, отдельная группа процессов, чтобы не оставлять внуков.
type Runtime struct {
	dataDir string
	cliPath string
	bindIP  string
	// probeAddr — адрес проб: bindIP, а для «все интерфейсы» — loopback.
	probeAddr string
	sec       *Secrets
	log       *slog.Logger

	mu     sync.Mutex
	procs  map[string]*process
	rings  map[string]*ring
	locks  map[string]*sync.Mutex
	closed bool

	// reload отдаёт актуальные набор и серверы для отложенного перезапуска:
	// снимок первого старта к моменту рестарта устаревает.
	reload func(id string) (*registry.Set, []registry.Server)
}

func NewRuntime(dataDir, cliPath, bindIP string, sec *Secrets, log *slog.Logger) *Runtime {
	if log == nil {
		log = slog.Default()
	}
	return &Runtime{
		dataDir:   dataDir,
		cliPath:   cliPath,
		bindIP:    bindIP,
		probeAddr: loopbackAddr(bindIP),
		sec:       sec,
		log:       log,
		procs:     map[string]*process{},
		rings:     map[string]*ring{},
		locks:     map[string]*sync.Mutex{},
	}
}

// loopbackAddr — адрес, по которому пробы достают набор: сам bindIP, а для
// «все интерфейсы» — соответствующий loopback.
func loopbackAddr(bindIP string) string {
	switch bindIP {
	case "", "0.0.0.0":
		return "127.0.0.1"
	case "::", "[::]":
		return "::1"
	}
	return bindIP
}

func (rt *Runtime) setLock(id string) *sync.Mutex {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	l := rt.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		rt.locks[id] = l
	}
	return l
}

func (rt *Runtime) ring(id string) *ring {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	r := rt.rings[id]
	if r == nil {
		r = &ring{}
		rt.rings[id] = r
	}
	return r
}

// Start поднимает набор, если он ещё не работает. Явный старт начинает бюджет
// перезапусков заново.
func (rt *Runtime) Start(ctx context.Context, set *registry.Set, servers []registry.Server) (RuntimeStatus, error) {
	l := rt.setLock(set.ID)
	l.Lock()
	defer l.Unlock()
	rt.mu.Lock()
	if cur := rt.procs[set.ID]; cur != nil {
		cur.restarts = 0
	}
	rt.mu.Unlock()
	return rt.startLocked(ctx, set, servers)
}

func (rt *Runtime) startLocked(ctx context.Context, set *registry.Set, servers []registry.Server) (RuntimeStatus, error) {
	rt.mu.Lock()
	cur := rt.procs[set.ID]
	closed := rt.closed
	rt.mu.Unlock()
	if closed {
		return RuntimeStatus{}, errors.New("менеджер остановлен")
	}
	if cur != nil && !cur.exited && !cur.stopping {
		return rt.status(set), nil
	}
	if !registry.CanServe(set) {
		return RuntimeStatus{}, fmt.Errorf("у набора %s нет активного ключа — выдайте новый", set.ID)
	}
	if err := portFree(listenHost(rt.bindIP), set.Port); err != nil {
		return RuntimeStatus{}, err
	}

	body, err := BuildSetConfig(set, setServers(set, servers), rt.bindIP)
	if err != nil {
		return RuntimeStatus{}, err
	}
	cfgPath, err := WriteSetConfig(rt.dataDir, set.ID, body)
	if err != nil {
		return RuntimeStatus{}, fmt.Errorf("конфиг набора %s: %w", set.ID, err)
	}

	logPath := LogPath(rt.dataDir, set.ID)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return RuntimeStatus{}, fmt.Errorf("лог набора %s: %w", set.ID, err)
	}
	ring := rt.ring(set.ID)
	size := int64(0)
	if info, err := logFile.Stat(); err == nil {
		size = info.Size()
	}
	writer := &logWriter{file: logFile, path: logPath, size: size, ring: ring}

	cmd := exec.Command(rt.cliPath, "serve", "--config", cfgPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Свой каталог состояния у каждого набора: общий oauth.json затирается
	// снимком другого процесса.
	cmd.Env = rt.setEnv(set, setServers(set, servers))
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return RuntimeStatus{}, fmt.Errorf("набор %s не стартовал: %w", set.ID, err)
	}

	restarts := 0
	if cur != nil {
		restarts = cur.restarts
	}
	p := &process{
		cmd:       cmd,
		pid:       cmd.Process.Pid,
		port:      set.Port,
		startedAt: time.Now(),
		restarts:  restarts,
		done:      make(chan struct{}),
	}
	rt.mu.Lock()
	rt.procs[set.ID] = p
	rt.mu.Unlock()

	go rt.wait(set.ID, set, servers, p, logFile)

	if !waitPort(rt.probeAddr, set.Port, readyBudget, p.done) {
		rt.mu.Lock()
		p.stopping = true
		p.err = fmt.Sprintf("порт %d не слушает", set.Port)
		rt.mu.Unlock()
		killGroup(p.pid, syscall.SIGKILL)
		<-p.done
		return RuntimeStatus{}, fmt.Errorf("набор %s не поднялся: порт %d не слушает", set.ID, set.Port)
	}
	_ = ctx
	return rt.status(set), nil
}

func (rt *Runtime) wait(id string, set *registry.Set, servers []registry.Server, p *process, logFile *os.File) {
	err := p.cmd.Wait()
	logFile.Close()

	code := -1
	if p.cmd.ProcessState != nil {
		code = p.cmd.ProcessState.ExitCode()
	}
	rt.mu.Lock()
	p.exited = true
	p.exitCode = code
	stopping := p.stopping
	closed := rt.closed
	restarts := p.restarts // снимок: ручной Start может сбросить поле под замком
	rt.mu.Unlock()
	close(p.done)

	if stopping || closed {
		return
	}
	if code == 0 {
		// Штатный выход — не падение: рестарта нет.
		return
	}
	if err != nil {
		rt.log.Warn("набор завершился", "set", id, "code", code, "err", err)
	}
	if restarts >= maxRestarts {
		rt.mu.Lock()
		p.err = fmt.Sprintf("набор падал %d раз, последний код %d", restarts+1, code)
		rt.mu.Unlock()
		return
	}
	delay := time.Second << restarts
	if delay > maxBackoff {
		delay = maxBackoff
	}
	rt.mu.Lock()
	p.restarts++
	p.err = fmt.Sprintf("упал (код %d), перезапуск %d/%d через %s", code, p.restarts, maxRestarts, delay)
	rt.mu.Unlock()

	go func() {
		time.Sleep(delay)
		l := rt.setLock(id)
		l.Lock()
		defer l.Unlock()
		rt.mu.Lock()
		same := rt.procs[id] == p && !p.stopping && !rt.closed
		rt.mu.Unlock()
		if !same {
			return
		}
		rs, fresh := set, servers
		if rt.reload != nil {
			freshSet, freshServers := rt.reload(id)
			if freshSet == nil {
				return // набор удалили, пока ждали
			}
			rs, fresh = freshSet, freshServers
		}
		if _, err := rt.startLocked(context.Background(), rs, fresh); err != nil {
			rt.log.Warn("перезапуск набора не удался", "set", id, "err", err)
		}
	}()
}

// Stop гасит набор: SIGTERM группе, через 5 с SIGKILL.
func (rt *Runtime) Stop(set *registry.Set) RuntimeStatus {
	l := rt.setLock(set.ID)
	l.Lock()
	defer l.Unlock()
	rt.mu.Lock()
	p := rt.procs[set.ID]
	rt.mu.Unlock()
	if p != nil {
		rt.kill(p, stopGrace)
		rt.mu.Lock()
		delete(rt.procs, set.ID)
		rt.mu.Unlock()
	}
	return rt.status(set)
}

// Restart — stop + start под той же блокировкой набора.
func (rt *Runtime) Restart(ctx context.Context, set *registry.Set, servers []registry.Server) (RuntimeStatus, error) {
	l := rt.setLock(set.ID)
	l.Lock()
	defer l.Unlock()
	if p := rt.proc(set.ID); p != nil {
		rt.kill(p, stopGrace)
		rt.mu.Lock()
		delete(rt.procs, set.ID)
		rt.mu.Unlock()
	}
	return rt.startLocked(ctx, set, servers)
}

func (rt *Runtime) proc(id string) *process {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.procs[id]
}

func (rt *Runtime) kill(p *process, grace time.Duration) {
	rt.mu.Lock()
	p.stopping = true
	rt.mu.Unlock()
	select {
	case <-p.done:
		return
	default:
	}
	killGroup(p.pid, syscall.SIGTERM)
	select {
	case <-p.done:
		return
	case <-time.After(grace):
	}
	killGroup(p.pid, syscall.SIGKILL)
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		rt.log.Warn("набор не подтвердил завершение после SIGKILL", "pid", p.pid)
	}
}

func killGroup(pid int, sig syscall.Signal) {
	if pid <= 1 {
		return
	}
	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(pid, sig)
	}
}

func (rt *Runtime) status(set *registry.Set) RuntimeStatus {
	rt.mu.Lock()
	p := rt.procs[set.ID]
	rt.mu.Unlock()
	out := RuntimeStatus{ID: set.ID, Port: set.Port}
	if p == nil {
		return out
	}
	rt.mu.Lock()
	out.Running = !p.exited && !p.stopping
	out.PID = p.pid
	out.StartedAt = p.startedAt.UTC().Format(time.RFC3339)
	out.Port = p.port
	out.Restarts = p.restarts
	out.Error = p.err
	if p.exited {
		code := p.exitCode
		out.ExitCode = &code
	}
	rt.mu.Unlock()
	return out
}

// Statuses — статус всех известных наборов по id.
func (rt *Runtime) Statuses() map[string]RuntimeStatus {
	rt.mu.Lock()
	ids := make([]string, 0, len(rt.procs))
	for id := range rt.procs {
		ids = append(ids, id)
	}
	rt.mu.Unlock()
	out := make(map[string]RuntimeStatus, len(ids))
	for _, id := range ids {
		rt.mu.Lock()
		p := rt.procs[id]
		rt.mu.Unlock()
		if p == nil {
			continue
		}
		rt.mu.Lock()
		out[id] = RuntimeStatus{
			ID:        id,
			Running:   !p.exited && !p.stopping,
			PID:       p.pid,
			StartedAt: p.startedAt.UTC().Format(time.RFC3339),
			Port:      p.port,
			Restarts:  p.restarts,
			Error:     p.err,
		}
		rt.mu.Unlock()
	}
	return out
}

// Logs — хвост файла лога набора; кольцо нужно, только пока файла нет.
func (rt *Runtime) Logs(id string, lines int) []string {
	if lines <= 0 {
		lines = 200
	}
	if out, err := tailFile(LogPath(rt.dataDir, id), lines); err == nil {
		return out
	}
	return rt.ring(id).tail(lines)
}

// tailFile — последние lines строк файла. Логи ротируются на 5 МиБ, поэтому
// читать файл целиком дешевле, чем возиться с seek.
func tailFile(path string, lines int) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(body), "\n")
	if text == "" {
		return []string{}, nil
	}
	all := strings.Split(text, "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return all, nil
}

// ServerLogs — строки лога, где имя сервера встречается отдельным словом.
func (rt *Runtime) ServerLogs(id, server string, lines int) []string {
	all := rt.Logs(id, 5000)
	out := make([]string, 0, 64)
	for _, line := range all {
		if mentions(line, server) {
			out = append(out, line)
		}
	}
	if lines > 0 && len(out) > lines {
		out = out[len(out)-lines:]
	}
	return out
}

// Close гасит все наборы, по 3 с на процесс. Блокировку набора ждёт, а не
// пропускает: иначе набор, который в этот момент стартует, остался бы сиротой.
func (rt *Runtime) Close() {
	rt.mu.Lock()
	rt.closed = true
	ids := make([]string, 0, len(rt.procs))
	for id := range rt.procs {
		ids = append(ids, id)
	}
	rt.mu.Unlock()
	sort.Strings(ids)
	for _, id := range ids {
		l := rt.setLock(id)
		acquired := make(chan struct{})
		go func() {
			l.Lock()
			close(acquired)
		}()
		timedOut := false
		select {
		case <-acquired:
		case <-time.After(readyBudget + stopGrace):
			timedOut = true
			rt.log.Warn("набор не отдал блокировку при остановке — гашу по известному pid", "set", id)
		}
		if p := rt.proc(id); p != nil {
			rt.kill(p, closeGrace)
		}
		if timedOut {
			// Старт ещё держит лок: отпустим его из горутины, когда закончит.
			go func() {
				<-acquired
				l.Unlock()
			}()
			continue
		}
		l.Unlock()
	}
}

// childEnv — whitelist плюс только те ${VAR}, на которые ссылается набор.
// Тот же хелпер отдаёт окружение probe и browse: у них в env не должно быть
// секретов менеджера.
func childEnv(servers []registry.Server, sec *Secrets) []string {
	values := map[string]string{}
	keep := map[string]bool{
		"PATH": true, "HOME": true, "LANG": true, "TZ": true, "TMPDIR": true,
		"NODE_EXTRA_CA_CERTS": true,
	}
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if keep[name] || strings.HasPrefix(name, "LC_") || strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "SSL_") {
			values[name] = value
		}
	}
	if sec != nil {
		for _, name := range envRefs(servers) {
			if v, ok := sec.Lookup(name); ok {
				values[name] = v
			}
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+values[name])
	}
	return out
}

// setEnv — окружение процесса набора: переменные серверов, свой ID и свой
// каталог состояния (лог и servers.json уже лежат в нём).
func (rt *Runtime) setEnv(set *registry.Set, servers []registry.Server) []string {
	return append(childEnv(servers, rt.sec), "MCPRIG_SET="+set.ID,
		"MCPRIG_DATA_DIR="+filepath.Join(rt.dataDir, "sets", set.ID))
}

// managerEnv — окружение stdio-сервера для probe и browse: whitelist плюс
// ${VAR} этого сервера, без секретов менеджера.
func managerEnv(sec *Secrets) func(*config.Server) []string {
	return func(srv *config.Server) []string {
		return childEnv([]registry.Server{srv}, sec)
	}
}

// waitPort ждёт слушателя; done закрывается, когда процесс умер, — ждать
// оставшийся бюджет незачем.
// listenHost — адрес, на котором набор слушает: пустой bindIP значит все
// интерфейсы.
func listenHost(bindIP string) string {
	switch bindIP {
	case "":
		return "0.0.0.0"
	case "[::]":
		return "::"
	}
	return bindIP
}

// portFree проверяет, что порт свободен: готовность набора проверяется
// подсоединением, и чужой слушатель выглядел бы поднявшимся набором.
func portFree(host string, port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("порт %d занят другим процессом", port)
	}
	return ln.Close()
}

func waitPort(host string, port int, budget time.Duration, done <-chan struct{}) bool {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	deadline := time.Now().Add(budget)
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return true
		}
		select {
		case <-done:
			return false
		default:
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(readyPoll)
	}
}

// ring — последние 2000 строк вывода набора для GET /api/sets/:id/logs.
type ring struct {
	mu      sync.Mutex
	lines   []string
	partial string
}

func (r *ring) add(chunk string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.partial += chunk
	for {
		i := strings.IndexByte(r.partial, '\n')
		if i < 0 {
			break
		}
		r.push(strings.TrimRight(r.partial[:i], "\r"))
		r.partial = r.partial[i+1:]
	}
	if len(r.partial) > partialCap {
		r.push(r.partial)
		r.partial = ""
	}
}

func (r *ring) push(line string) {
	r.lines = append(r.lines, line)
	if len(r.lines) > ringLines {
		r.lines = append(r.lines[:0], r.lines[len(r.lines)-ringLines:]...)
	}
}

func (r *ring) tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > len(r.lines) {
		n = len(r.lines)
	}
	out := make([]string, n)
	copy(out, r.lines[len(r.lines)-n:])
	return out
}

// logWriter пишет и в файл, и в кольцо: два потока ребёнка зовут Write
// параллельно. Файл ротируется по размеру, иначе долгоживущий набор растёт без
// предела.
type logWriter struct {
	mu   sync.Mutex
	file *os.File
	path string
	size int64
	ring *ring
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > maxLogBytes {
		w.rotate()
	}
	if w.file != nil {
		if _, err := w.file.Write(p); err != nil {
			return 0, err
		}
		w.size += int64(len(p))
	}
	w.ring.add(string(p))
	return len(p), nil
}

// rotate заменяет .log на свежий файл, оставляя один .log.1.
func (w *logWriter) rotate() {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	_ = os.Rename(w.path, w.path+".1")
	if f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		w.file = f
	}
	w.size = 0
}

var _ io.Writer = (*logWriter)(nil)
