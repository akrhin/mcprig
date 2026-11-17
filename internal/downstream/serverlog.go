package downstream

import (
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/safepath"
)

// ServerLogs пишет вывод процессов серверов на диск: набор живёт отдельным
// процессом, и панель иначе не увидит ни одной строки ребёнка — stdout у
// stdio-сервера занят протоколом, а stderr доходил только до хвоста ошибки
// старта в памяти. nil у Connector — не писать.
type ServerLogs struct {
	setDir string
	mu     sync.Mutex
	files  map[string]*serverLog
}

// serverLogLimit — сколько байт вывода держим на сервер. Хвост читается окном
// в 64 КиБ, так что больше за один ответ панель всё равно не получит.
const serverLogLimit = 256 << 10

// ServerLogPath — <dir>/serverlogs/<server>.log: файл хвоста одного сервера.
func ServerLogPath(dir, name string) string {
	return filepath.Join(dir, "serverlogs", name+".log")
}

// NewServerLogs заводит приёмник вывода для каталога набора (там, где лежит
// его servers.json).
func NewServerLogs(setDir string) *ServerLogs {
	return &ServerLogs{setDir: setDir, files: map[string]*serverLog{}}
}

// Writer — приёмник stderr одного сервера. Файл открывается один раз на
// процесс: переподключение сервера дописывает его же, а не начинает заново.
// nil — сервер без годного имени не логируется.
func (l *ServerLogs) Writer(srv *config.Server) io.Writer {
	if l == nil || srv == nil || !safepath.Name(srv.Name) {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if w := l.files[srv.Name]; w != nil {
		return w
	}
	path := ServerLogPath(l.setDir, srv.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	w := &serverLog{file: f}
	if info, err := f.Stat(); err == nil {
		w.size = info.Size()
	}
	l.files[srv.Name] = w
	return w
}

// serverLog — stderr одного процесса с пределом размера.
type serverLog struct {
	mu   sync.Mutex
	file *os.File
	size int64
}

func (w *serverLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// shortcut: при переполнении хвост обрезается целиком, а не ротацией —
	// старые строки теряются разом, зато файл не растёт без предела.
	if w.size+int64(len(p)) > serverLogLimit {
		if err := w.file.Truncate(0); err == nil {
			w.size = 0
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	// Ошибка записи не возвращается: stdout/stderr ребёнка — диагностика, и
	// сбой диска не должен ломать его Wait.
	if err != nil {
		return len(p), nil
	}
	return n, nil
}
