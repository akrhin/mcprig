package downstream

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/safepath"
)

// ServerLogs пишет вывод процессов серверов на диск: набор живёт отдельным
// процессом, и панель иначе не увидит ни одной строки ребёнка — stdout у
// stdio-сервера занят протоколом, а stderr доходил только до хвоста ошибки
// старта в памяти. nil у Connector — не писать.
type ServerLogs struct {
	log    *slog.Logger
	setDir string
	mu     sync.Mutex
	files  map[string]*serverLog
}

// warn пишет сбой хвоста в лог набора: без этой строки пустой лог сервера
// не отличить от неработающего диска.
func (l *ServerLogs) warn(what, server string, err error) {
	if l.log == nil {
		return
	}
	if err != nil {
		l.log.Warn("хвост сервера: "+what, "server", server, "err", err)
		return
	}
	l.log.Warn("хвост сервера: "+what, "server", server)
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
func NewServerLogs(setDir string, log *slog.Logger) *ServerLogs {
	return &ServerLogs{setDir: setDir, log: log, files: map[string]*serverLog{}}
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
		// Без строки здесь «лог сервера пуст» не отличить от сломанного диска.
		l.warn("каталог хвоста не создан", srv.Name, err)
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		l.warn("файл хвоста не открыт", srv.Name, err)
		return nil
	}
	// режим у существующего файла OpenFile не меняет, а хвост не должен быть
	// читаем шире владельца даже после ручной правки прав
	if err := f.Chmod(0o600); err != nil {
		l.warn("права хвоста не поправлены", srv.Name, err)
	}
	w := &serverLog{file: f, log: l.log, server: srv.Name}
	if info, err := f.Stat(); err == nil {
		w.size = info.Size()
	} else {
		l.warn("размер хвоста не прочитан", srv.Name, err)
	}
	// Метка запуска: без неё перезапуск после падения не отличить от первого
	// старта, а хвост прошлого запуска выглядит как текущий.
	if w.size == 0 {
		if _, err := f.WriteString("— запуск " + time.Now().Format("2006-01-02 15:04:05") + " —\n"); err == nil {
			w.size = int64(len("— запуск 2006-01-02 15:04:05 —\n"))
		}
	}
	l.files[srv.Name] = w
	return w
}

// serverLog — stderr одного процесса с пределом размера.
type serverLog struct {
	mu     sync.Mutex
	file   *os.File
	size   int64
	log    *slog.Logger
	server string
	broken bool // обрезка не удалась: дописывать нельзя, иначе файл растёт без предела
}

func (w *serverLog) warn(what string) {
	if w.log == nil {
		return
	}
	w.log.Warn("хвост сервера: "+what, "server", w.server)
}

func (w *serverLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// shortcut: при переполнении хвост обрезается целиком, а не ротацией —
	// старые строки теряются разом, зато файл не растёт без предела.
	// Один Write может прийти больше лимита: пишем хвост, иначе файл
	// перескочит потолок и там и останется.
	if int64(len(p)) > serverLogLimit {
		p = p[len(p)-int(serverLogLimit):]
	}
	if w.size+int64(len(p)) > serverLogLimit {
		if err := w.file.Truncate(0); err == nil {
			w.size = 0
		} else {
			w.broken = true
			w.warn("хвост не обрезан, запись остановлена")
		}
	}
	if w.broken {
		return len(p), nil
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	// Ошибка записи не возвращается: stdout/stderr ребёнка — диагностика, и
	// сбой диска не должен ломать его Wait.
	if err != nil {
		w.warn("хвост не записан")
		return len(p), nil
	}
	return n, nil
}
