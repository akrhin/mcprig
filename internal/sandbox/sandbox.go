// Package sandbox wraps a stdio server's argv in bubblewrap: its own mount
// tree, read-only root, no /home, no network. Transport does not change —
// stdin/stdout still belong to the process, so mcp.CommandTransport works.
package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sintez/mcprig/internal/config"
)

// available проверяет, что bwrap действительно запускается: бинарь есть и
// userns разрешён. Переменная, а не функция в вызове, — тесты подменяют.
var available = probe

// probe — короткий реальный запуск: LookPath не отличает «нет userns» от
// «всё хорошо», а fail-closed требует знать ответ до спавна сервера.
func probe() error {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return errors.New("bwrap не найден в PATH")
	}
	// Пробный запуск несёт минимум привязок: без них корень — пустой tmpfs,
	// и "execvp /bin/true: No such file or directory" означало бы не отказ
	// userns, а пустую песочницу.
	cmd := exec.Command(path, "--unshare-all", "--die-with-parent",
		"--ro-bind-try", "/usr", "/usr",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--", "/usr/bin/true")
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bwrap не запускается (userns выключен?): %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Wrap возвращает argv для запуска сервера. mode "none" (или нет блока) —
// argv как есть; mode "bwrap" — префикс песочницы. Недоступная песочница при
// onUnavailable "deny" (умолчание) — ошибка: сервер не стартует.
func Wrap(srv *config.Server, argv []string) ([]string, error) {
	sb := srv.Sandbox
	if sb == nil || sb.Mode == config.SandboxModeNone {
		return argv, nil
	}
	if len(argv) == 0 {
		return nil, errors.New("sandbox: пустая команда")
	}
	if err := available(); err != nil {
		switch sb.OnUnavailable {
		case config.OnUnavailableWarn:
			// warn: сервер живёт без изоляции, но об этом видно в логе.
			slog.Warn("песочница недоступна, сервер стартует без неё", "server", srv.Name, "причина", err)
			return argv, nil
		case config.OnUnavailableAllow:
			return argv, nil
		default:
			return nil, fmt.Errorf("sandbox unavailable: %w", err)
		}
	}
	return bwrapArgv(srv, sb, argv)
}

func bwrapArgv(srv *config.Server, sb *config.Sandbox, argv []string) ([]string, error) {
	args := []string{"bwrap"}

	// Пути из конфига становятся аргументами bwrap, и значение, похожее на
	// флаг, стало бы флагом: только абсолютные пути, каждый — один элемент
	// argv (пробелы внутри не разбивают: exec ничего не склеивает).
	type mount struct{ flag, src string }
	var mounts []mount
	at := make(map[string]int)
	bind := func(flag, src string) error {
		if !filepath.IsAbs(src) || len(src) == 0 {
			return fmt.Errorf("sandbox: путь %q должен быть абсолютным", src)
		}
		if _, err := os.Stat(src); err != nil {
			// bwrap падает на отсутствующем источнике — пропускаем молча:
			// /lib64 есть не везде.
			return nil
		}
		if i, ok := at[src]; ok {
			// Один путь в обоих списках: rw сильнее ro.
			if flag == "--bind" {
				mounts[i].flag = flag
			}
			return nil
		}
		at[src] = len(mounts)
		mounts = append(mounts, mount{flag, src})
		return nil
	}

	// Базовые монтирования первыми: ro/rw-путь из конфига может лежать под
	// /tmp, и --tmpfs /tmp поверх него спрятал бы привязку.
	args = append(args, "--tmpfs", "/tmp", "--proc", "/proc", "--dev", "/dev")

	for _, p := range sb.RO {
		if err := bind("--ro-bind", p); err != nil {
			return nil, err
		}
	}
	for _, p := range sb.RW {
		if err := bind("--bind", p); err != nil {
			return nil, err
		}
	}
	// Рабочая папка сервера: без бинда --chdir внутри песочницы некуда.
	if srv.Cwd != "" {
		if err := bind("--ro-bind", srv.Cwd); err != nil {
			return nil, err
		}
	}
	// Родитель монтируется раньше ребёнка: бинд родителя поверх уже
	// смонтированного ребёнка его прячет (rw-каталог под ro-корнем молча
	// становился read-only).
	sort.SliceStable(mounts, func(i, j int) bool { return strings.Count(mounts[i].src, "/") < strings.Count(mounts[j].src, "/") })
	for _, m := range mounts {
		args = append(args, m.flag, m.src, m.src)
	}
	// Корень — tmpfs песочницы; ro делает запись в / отказом, а не тихой
	// записью в приватный tmpfs (иначе тест «запись в /» ничего не проверяет).
	args = append(args, "--remount-ro", "/")
	args = append(args, "--unshare-user", "--unshare-pid", "--unshare-ipc",
		"--unshare-uts", "--unshare-cgroup-try")
	if sb.Network != config.SandboxNetworkAllow {
		args = append(args, "--unshare-net")
	}
	args = append(args, "--die-with-parent", "--new-session")
	chdir := srv.Cwd
	if chdir == "" {
		chdir = "/"
	} else if !filepath.IsAbs(chdir) {
		return nil, fmt.Errorf("sandbox: cwd %q должен быть абсолютным", chdir)
	}
	args = append(args, "--chdir", chdir, "--setenv", "TMPDIR", "/tmp")

	// prlimit — внутри userns, а не снаружи: RLIMIT_NPROC считается по uid в
	// пространстве имён, и снаружи лимит упирается в уже живые процессы
	// владельца (bwrap не может создать namespace).
	if limits := limitArgs(sb); len(limits) > 0 {
		args = append(args, "--", "prlimit")
		args = append(args, limits...)
		args = append(args, "--")
	} else {
		args = append(args, "--")
	}
	return append(args, argv...), nil
}

// limitArgs — флаги prlimit. --as принимает только байты: суффикс "M"
// util-linux здесь не разбирает.
func limitArgs(sb *config.Sandbox) []string {
	var out []string
	if sb.MemoryMb > 0 {
		out = append(out, fmt.Sprintf("--as=%d", int64(sb.MemoryMb)*1024*1024))
	}
	if sb.Pids > 0 {
		out = append(out, fmt.Sprintf("--nproc=%d", sb.Pids))
	}
	return out
}

// Probe сообщает, доступна ли песочница на этом хосте: bwrap есть и userns
// разрешён. Тесты и doctor спрашивают то же самое, что проверяет Wrap.
func Probe() error { return available() }
