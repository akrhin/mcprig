package downstream

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// groupTransport — mcp.CommandTransport, который на закрытии гасит всю группу
// процесса, а не только прямого ребёнка. Сервер вида
// sh -c "npx … & wait" оставлял в системе внуков: проба возвращала таймаут, а
// sleep оставался жить, переподвешенный к init.
type groupTransport struct {
	mcp.CommandTransport
}

func (t *groupTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.CommandTransport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &groupConn{Connection: conn, ctx: ctx, pgid: t.Command.Process.Pid}, nil
}

// groupConn закрывает соединение вместе с группой: сигнал транспорта достаётся
// только прямому ребёнку, внуки переживают его.
type groupConn struct {
	mcp.Connection
	ctx  context.Context // контекст подключения: истёк — ждать нормального выхода нечего
	pgid int
}

func (c *groupConn) Close() error {
	done := make(chan error, 1)
	go func() { done <- c.Connection.Close() }()
	grace := closeGrace
	if c.ctx.Err() != nil {
		// Бюджет подключения уже истёк (сервер висел на initialize): ждать
		// нормального выхода нечего, сигнал группе уходит сразу.
		grace = 0
	}
	select {
	case err := <-done:
		killGroup(c.pgid)
		return err
	case <-time.After(grace):
	}
	// Сервер не вышел после закрытия stdin: сигнал всей группе, иначе внуки
	// доживают до SIGKILL набора и остаются сиротами.
	_ = syscall.Kill(-c.pgid, syscall.SIGTERM)
	select {
	case err := <-done:
		killGroup(c.pgid)
		return err
	case <-time.After(groupGrace):
		_ = syscall.Kill(-c.pgid, syscall.SIGKILL)
		return nil // транспорт ещё ждёт своего ребёнка, но группа уже погашена
	}
}

// closeGrace — сколько сервер выходит сам после закрытия stdin, прежде чем
// сигнал уйдёт группе. Ниже stopGrace менеджера (5 с): иначе перезапуск набора
// успевает снести его раньше, чем сервер закрылся.
const closeGrace = 3 * time.Second

// groupGrace — сколько группа живёт после сигнала, прежде чем получит следующий.
const groupGrace = time.Second

// killGroup гасит группу процесса: SIGTERM, короткое ожидание, SIGKILL.
// Прямого ребёнка к этому моменту уже погасил транспорт, здесь добиваются внуки.
func killGroup(pgid int) {
	if pgid <= 1 || groupGone(pgid, 0) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if groupGone(pgid, groupGrace) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	groupGone(pgid, groupGrace)
}

// groupGone ждёт исчезновения группы не дольше d и отвечает, исчезла ли она.
func groupGone(pgid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if !groupAlive(pgid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// groupAlive отвечает, есть ли в группе живой процесс. Зомби не считаются:
// kill(0) считает их живыми, а пожинает их чужой PID 1 — в контейнере он этого
// не делает, и закрытие ждало бы весь бюджет впустую.
func groupAlive(pgid int) bool {
	if err := syscall.Kill(-pgid, 0); err != nil {
		return false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true // группу не посмотреть — считаем живой, бюджет всё равно ограничен
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if state, _, grp, ok := procInfo(pid); ok && grp == pgid && state != "Z" {
			return true
		}
	}
	return false
}

// procInfo читает состояние, родителя и группу процесса. comm в скобках может
// содержать пробелы, поэтому поля считаются после последней «) ».
func procInfo(pid int) (state string, ppid, pgid int, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, 0, false
	}
	s := string(b)
	i := strings.LastIndex(s, ") ")
	if i < 0 {
		return "", 0, 0, false
	}
	f := strings.Fields(s[i+2:])
	if len(f) < 3 {
		return "", 0, 0, false
	}
	ppid, _ = strconv.Atoi(f[1])
	pgid, _ = strconv.Atoi(f[2])
	return f[0], ppid, pgid, true
}
