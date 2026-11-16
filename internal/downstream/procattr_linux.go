//go:build linux

package downstream

import "syscall"

// childAttr — атрибуты прямого ребёнка набора: своя группа и сигнал смерти
// родителя. Группа нужна, чтобы погасить внуков; Pdeathsig ядро шлёт сторожу,
// когда процесс набора убит, в том числе SIGKILL.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
}
