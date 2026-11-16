package downstream

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// argSep разделяет argv сторожа в env: env не умеет NUL.
const argSep = "\x1f"

// TestMain перехватывает скрытый режим child-guard: набор спавнит сторожем
// os.Executable(), а в тестах это тестовый бинарник.
func TestMain(m *testing.M) {
	RunChildGuardMode(os.Args)
	os.Exit(m.Run())
}

// guardCmd собирает запуск сторожа так, как это делает набор.
func guardCmd(t *testing.T, argv ...string) *exec.Cmd {
	t.Helper()
	line := append([]string{os.Args[0], "child-guard", "--"}, argv...)
	cmd := exec.Command(line[0], line[1:]...)
	cmd.Env = append(os.Environ(), "GUARD_ARGV="+strings.Join(argv, argSep))
	return cmd
}

// TestGuardKillsTreeWhenParentDies: набор убит SIGKILL, обработчика у него нет.
// Сторож получает SIGTERM по Pdeathsig и гасит группу: и sh, и его sleep
// исчезают, а без сторожа они переживали бы набор.
func TestGuardKillsTreeWhenParentDies(t *testing.T) {
	parent := exec.Command(os.Args[0], "-test.run=TestGuardParentProcess")
	parent.Env = append(os.Environ(),
		"GO_WANT_GUARD_PARENT=1",
		"GUARD_ARGV="+strings.Join([]string{"/bin/sh", "-c", "sleep 300 & wait"}, argSep))
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	ppid := parent.Process.Pid
	// Родитель сторожа (этот процесс) жив: смерть по SIGKILL ниже заставит
	// ядро послать сторожу SIGTERM, и группа погаснет каскадом.
	t.Cleanup(func() { _ = parent.Process.Kill() })

	tree := waitTree(t, ppid, 4) // родитель, сторож, sh, sleep
	if err := parent.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL родителя: %v", err)
	}
	_ = parent.Wait()
	for _, p := range tree[1:] {
		if !gone(p, 2*time.Second) {
			t.Errorf("процесс %d жив после SIGKILL родителя", p)
		}
	}
}

// TestGuardParentProcess — не тест: родитель сторожа в том виде, как его
// создаёт набор (своя группа, Pdeathsig). Запускается из
// TestGuardKillsTreeWhenParentDies и гибнет там от SIGKILL.
func TestGuardParentProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GUARD_PARENT") != "1" {
		t.Skip("служебный процесс: запускается из TestGuardKillsTreeWhenParentDies")
	}
	line := append([]string{os.Args[0], "child-guard", "--"}, strings.Split(os.Getenv("GUARD_ARGV"), argSep)...)
	cmd := exec.Command(line[0], line[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
	if err := cmd.Start(); err != nil {
		os.Exit(9)
	}
	_ = cmd.Wait() // ждём сторожа, пока тест не убьёт нас SIGKILL
}

// TestGuardKeepsChildExitCode: нормальный выход ребёнка — тот же код наружу.
func TestGuardKeepsChildExitCode(t *testing.T) {
	err := guardCmd(t, "/bin/sh", "-c", "exit 3").Run()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("ждали код 3, получили %v", err)
	}
	if code := ee.ExitCode(); code != 3 {
		t.Fatalf("сторож вернул %d, ждали 3", code)
	}
}

// TestGuardTermKillsGroup: SIGTERM сторожу (так его зовёт ядро по Pdeathsig)
// гасит группу целиком.
func TestGuardTermKillsGroup(t *testing.T) {
	cmd := guardCmd(t, "/bin/sh", "-c", "sleep 300 & wait")
	// Своя группа — как у сторожа набора; иначе kill(-pgid) задел бы группу
	// теста.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	guardPid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-guardPid, syscall.SIGKILL) })

	tree := waitTree(t, guardPid, 3) // сторож, sh, sleep
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	// Сторож гасит группу SIGKILL вместе с собой: смерть по сигналу — норма.
	code := cmd.ProcessState.ExitCode()
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code = 128 + int(ws.Signal())
	}
	if code != 0 && code != 143 && code != 137 {
		t.Errorf("сторож вышел с кодом %d, ждали 0/143 (группа погашена)", code)
	}
	for _, p := range tree[1:] {
		if !gone(p, 2*time.Second) {
			t.Errorf("процесс %d жив после SIGTERM сторожу", p)
		}
	}
}
