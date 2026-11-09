package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sintez/mcprig/internal/config"
)

// stubAvailable подменяет проверку bwrap: юнит-тесты не зависят от хоста.
func stubAvailable(t *testing.T, err error) {
	t.Helper()
	old := available
	available = func() error { return err }
	t.Cleanup(func() { available = old })
}

func sandboxed(ro ...string) *config.Server {
	return &config.Server{
		Name:      "victim",
		Transport: config.TransportStdio,
		Command:   []string{"node", "server.js", "--port", "1"},
		Sandbox: &config.Sandbox{
			Mode:       config.SandboxModeBwrap,
			Network:    config.SandboxNetworkNone,
			MemoryMb:   config.DefaultSandboxMemoryMb,
			Pids:       config.DefaultSandboxPids,
			TimeoutSec: config.DefaultSandboxTimeout,
			RO:         append(append([]string{}, config.DefaultSandboxRO...), ro...),
		},
	}
}

// hasSeq ищет последовательность аргументов: флаги bwrap идут парами, и
// «есть в argv» значит «именно в таком порядке».
func hasSeq(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		match := true
		for j := range seq {
			if args[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestWrapNoSandbox(t *testing.T) {
	stubAvailable(t, nil)
	srv := &config.Server{Name: "plain", Command: []string{"echo", "hi"}}
	got, err := Wrap(srv, srv.Command)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if strings.Join(got, " ") != "echo hi" {
		t.Fatalf("argv changed: %v", got)
	}

	srv.Sandbox = &config.Sandbox{Mode: config.SandboxModeNone}
	if got, err = Wrap(srv, srv.Command); err != nil || strings.Join(got, " ") != "echo hi" {
		t.Fatalf("mode none: %v %v", got, err)
	}
}

func TestWrapBwrapArgv(t *testing.T) {
	stubAvailable(t, nil)
	srv := sandboxed()

	got, err := Wrap(srv, srv.Command)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if got[0] != "bwrap" {
		t.Fatalf("argv не начинается с bwrap: %v", got)
	}
	for _, want := range [][]string{
		{"--ro-bind", "/usr", "/usr"},
		{"--tmpfs", "/tmp"},
		{"--proc", "/proc"},
		{"--remount-ro", "/"},
		{"--unshare-net"},
		{"--die-with-parent", "--new-session"},
		{"--chdir", "/"},
		{"--setenv", "TMPDIR", "/tmp"},
		{"--", "prlimit", "--as=536870912", "--nproc=256", "--", "node", "server.js", "--port", "1"},
	} {
		if !hasSeq(got, want...) {
			t.Errorf("нет %v в %v", want, got)
		}
	}
}

func TestWrapNetworkAllow(t *testing.T) {
	stubAvailable(t, nil)
	srv := sandboxed()
	srv.Sandbox.Network = config.SandboxNetworkAllow

	got, err := Wrap(srv, srv.Command)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if hasSeq(got, "--unshare-net") {
		t.Fatalf("network allow, но сеть отрезана: %v", got)
	}
}

func TestWrapRWAndCwd(t *testing.T) {
	stubAvailable(t, nil)
	dir := t.TempDir()
	srv := sandboxed()
	srv.Sandbox.RW = []string{dir}
	srv.Cwd = dir

	got, err := Wrap(srv, srv.Command)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if !hasSeq(got, "--bind", dir, dir) {
		t.Errorf("rw не привязан: %v", got)
	}
	if !hasSeq(got, "--chdir", dir) {
		t.Errorf("cwd не стал --chdir: %v", got)
	}
	// cwd уже привязан rw — второй ro-bind того же пути не нужен.
	if n := strings.Count(strings.Join(got, "\x00"), dir); n != 3 {
		t.Errorf("путь cwd встречается %d раз: %v", n, got)
	}
}

func TestWrapChildBindAfterParent(t *testing.T) {
	stubAvailable(t, nil)
	parent := t.TempDir()
	child := filepath.Join(parent, "out")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := sandboxed(parent)
	srv.Sandbox.RW = []string{child}

	got, err := Wrap(srv, srv.Command)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if !hasSeq(got, "--ro-bind", parent, parent) {
		t.Fatalf("родитель не привязан ro: %v", got)
	}
	// Ребёнок монтируется после родителя, иначе бинд родителя его прячет:
	// rw-каталог молча становился read-only.
	if !hasSeq(got, "--bind", child, child) {
		t.Fatalf("ребёнок не привязан rw: %v", got)
	}
	if strings.Index(strings.Join(got, "\x00"), child) < strings.Index(strings.Join(got, "\x00"), parent+"\x00") {
		t.Errorf("ребёнок смонтирован раньше родителя: %v", got)
	}
}

func TestWrapFailClosed(t *testing.T) {
	stubAvailable(t, errors.New("нет userns"))

	srv := sandboxed()
	if _, err := Wrap(srv, srv.Command); err == nil || !strings.Contains(err.Error(), "sandbox unavailable") {
		t.Fatalf("deny должен отбить запуск, получено %v", err)
	}

	srv.Sandbox.OnUnavailable = config.OnUnavailableWarn
	got, err := Wrap(srv, srv.Command)
	if err != nil || strings.Join(got, " ") != strings.Join(srv.Command, " ") {
		t.Fatalf("warn должен вернуть argv как есть: %v %v", got, err)
	}

	srv.Sandbox.OnUnavailable = config.OnUnavailableAllow
	if got, err = Wrap(srv, srv.Command); err != nil || len(got) != len(srv.Command) {
		t.Fatalf("allow должен вернуть argv как есть: %v %v", got, err)
	}
}

// Сборка argv — attack surface (CVE-2026-55887): значение из конфига не
// должно стать флагом bwrap, а имя с пробелом — разъехаться на два аргумента.
func TestWrapPathsAreNotFlags(t *testing.T) {
	stubAvailable(t, nil)

	spaced := filepath.Join(t.TempDir(), "a b")
	if err := os.MkdirAll(spaced, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := sandboxed(spaced)
	got, err := Wrap(srv, srv.Command)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if !hasSeq(got, "--ro-bind", spaced, spaced) {
		t.Fatalf("путь с пробелом не дошёл одним аргументом: %v", got)
	}

	for _, bad := range []string{"--dev-bind", "-x", "relative/dir"} {
		srv := sandboxed(bad)
		if _, err := Wrap(srv, srv.Command); err == nil {
			t.Fatalf("путь %q принят как флаг", bad)
		}
	}
}

func TestWrapMissingPathSkipped(t *testing.T) {
	stubAvailable(t, nil)
	missing := filepath.Join(t.TempDir(), "нет-такого")
	srv := sandboxed(missing)

	got, err := Wrap(srv, srv.Command)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if hasSeq(got, "--ro-bind", missing, missing) {
		t.Fatalf("несуществующий источник попал в argv (bwrap на нём падает): %v", got)
	}
}

func TestWrapRejectsRelativeCwd(t *testing.T) {
	stubAvailable(t, nil)
	srv := sandboxed()
	srv.Cwd = "relative"
	if _, err := Wrap(srv, srv.Command); err == nil {
		t.Fatal("относительный cwd должен отбиваться")
	}
}
