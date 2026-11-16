package downstream

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// GuardMain — скрытый режим child-guard: запускает сервер набора своим
// ребёнком и гасит его группу, когда набор умирает.
//
// Прямого ребёнка набора (этого сторожа) родитель создаёт с Pdeathsig SIGTERM:
// ядро присылает его, когда процесс набора убит, в том числе SIGKILL. У
// убитого процесса обработчика быть не может, а сторож переживает его и
// добивает внуков вида sh -c "npx … & wait".
//
// Сторож остаётся в группе, которую создал родитель (Setpgid; setsid не
// вызывать), иначе kill(-pgid) не достанет внуков.
func GuardMain(argv []string, stderr io.Writer) int {
	// Раньше всего: SIGTERM, пришедший до установки обработчика, убил бы
	// сторожа молча и оставил группу жить.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	defer signal.Stop(sigs)

	// Разделитель режима: mcprig child-guard -- <команда> [аргументы...].
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		fmt.Fprintln(stderr, "child-guard: не указана команда")
		return 2
	}

	pgid, err := syscall.Getpgid(0)
	if err != nil {
		fmt.Fprintln(stderr, "child-guard: группа процессов:", err)
		return 1
	}
	// Родитель умер до старта: Pdeathsig уже некому послать.
	if os.Getppid() == 1 {
		killGuardGroup(pgid, nil)
		return 1
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	// Наследование, а не проксирование: stdin/stdout — канал протокола.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(stderr, "child-guard:", err)
		return 127
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-sigs:
		// Набор умер или закрылся: гасим группу целиком, вместе с собой.
		killGuardGroup(pgid, cmd.Process)
		<-done
		return 143
	case <-done:
		// Нормальный выход ребёнка: тот же код наружу.
		return cmd.ProcessState.ExitCode()
	}
}

// RunChildGuardMode выполняет скрытый режим child-guard, если argv его просит,
// и завершает процесс: режим владеет процессом целиком. Через него набор
// спавнит сторожем os.Executable(), поэтому его же зовут TestMain пакетов, чьи
// тесты поднимают stdio-серверы (в тестах os.Executable() — тестовый бинарник).
func RunChildGuardMode(args []string) {
	if len(args) > 1 && args[1] == "child-guard" {
		os.Exit(GuardMain(args[2:], os.Stderr))
	}
}

// killGuardGroup гасит группу сторожа. Если сторож не её лидер (родитель не
// поставил Setpgid), kill(-pgid) задел бы чужую группу — в том числе сам
// набор, — поэтому тогда добивается только прямой ребёнок.
func killGuardGroup(pgid int, child *os.Process) {
	if pgid == os.Getpid() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	if child != nil {
		_ = child.Kill()
	}
}
