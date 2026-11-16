//go:build !linux

package downstream

import "syscall"

// childAttr — своя группа процесса. Сигнала смерти родителя вне Linux нет,
// поэтому пережившего набор сторожа забирает с собой группа набора при
// нормальном закрытии, а при SIGKILL внуки остаются — так же было до 0.6.0.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
