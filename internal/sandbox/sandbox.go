// Package sandbox wraps a stdio server's argv in bubblewrap: its own mount
// tree, read-only root, no /home, no network. Transport does not change —
// stdin/stdout still belong to the process, so mcp.CommandTransport works.
package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/sintez/mcprig/internal/config"
)

// available проверяет, что bwrap действительно запускается: бинарь есть и
// userns разрешён. Переменная, а не функция в вызове, — тесты подменяют.
var available = probe

// probe — короткий реальный запуск: LookPath не отличает «нет userns» от
// «всё хорошо», а fail-closed требует знать ответ до спавна сервера. Успех
// кэшируется: повторный запуск на каждый сервер стоил бы процесса.
var probeOnce struct {
	once sync.Once
	err  error
}

func probe() error {
	probeOnce.once.Do(func() { probeOnce.err = probeRun() })
	return probeOnce.err
}

func probeRun() error {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return errors.New("bwrap не найден в PATH")
	}
	// Не жёсткое /usr/bin/true: на хосте без /usr пустая песочница выглядела
	// бы как выключенный userns.
	trueBin, err := exec.LookPath("true")
	if err != nil {
		return errors.New("нечем проверить песочницу: true не найден в PATH")
	}
	// /bin/true — обычно симлинк в /usr/bin: внутрь привязан только /usr, и
	// непереход по ссылке давал «exec внутри песочницы не удался» на живом
	// userns (проверено с PATH=/home/sintez/.local/bin:/bin).
	if real, err := filepath.EvalSymlinks(trueBin); err == nil {
		trueBin = real
	}
	// Пробный запуск несёт минимум привязок: без них корень — пустой tmpfs,
	// и "execvp: No such file or directory" означало бы не отказ userns, а
	// пустую песочницу.
	cmd := exec.Command(bwrap, "--unshare-all", "--die-with-parent",
		"--ro-bind-try", "/usr", "/usr",
		"--ro-bind-try", "/bin", "/bin",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--", trueBin)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "execvp") {
			return fmt.Errorf("exec внутри песочницы не удался (%s): %s", trueBin, msg)
		}
		return fmt.Errorf("bwrap не создаёт namespace (userns выключен?): %v: %s", err, msg)
	}
	return nil
}

// Result — что Wrap сделал с argv. Applied=false при mode "none" — это норма
// (изоляция не запрошена), а Reason заполнен только когда песочница
// запрошена, но не применена: без этого деградация была видна лишь в логе
// пакета, а health и list_servers считали сервер здоровым.
type Result struct {
	Argv    []string
	Applied bool
	Reason  string
}

// Wrap возвращает argv для запуска сервера. mode "none" (или нет блока) —
// argv как есть; mode "bwrap" — префикс песочницы. Недоступная песочница при
// onUnavailable "deny" (умолчание) — ошибка: сервер не стартует.
func Wrap(srv *config.Server, argv []string) (Result, error) {
	sb := srv.Sandbox
	if sb == nil || sb.Mode == config.SandboxModeNone {
		return Result{Argv: argv}, nil
	}
	if len(argv) == 0 {
		return Result{}, errors.New("sandbox: пустая команда")
	}
	if err := available(); err != nil {
		switch sb.OnUnavailable {
		case config.OnUnavailableWarn:
			// warn: сервер живёт без изоляции, но об этом видно в логе и в
			// list_servers — причина возвращается вызывающему.
			return Result{Argv: argv, Reason: err.Error()}, nil
		case config.OnUnavailableAllow:
			return Result{Argv: argv, Reason: err.Error()}, nil
		default:
			return Result{}, fmt.Errorf("sandbox unavailable: %w", err)
		}
	}
	wrapped, err := bwrapArgv(srv, sb, argv)
	if err != nil {
		return Result{}, err
	}
	return Result{Argv: wrapped, Applied: true}, nil
}

func bwrapArgv(srv *config.Server, sb *config.Sandbox, argv []string) ([]string, error) {
	args := []string{"bwrap"}

	// Пути из конфига становятся аргументами bwrap, и значение, похожее на
	// флаг, стало бы флагом: только абсолютные пути, каждый — один элемент
	// argv (пробелы внутри не разбивают: exec ничего не склеивает).
	type mount struct{ flag, src string }
	var mounts []mount
	at := make(map[string]int)
	bind := func(flag, src string, optional bool) error {
		if !filepath.IsAbs(src) || len(src) == 0 {
			return fmt.Errorf("sandbox: путь %q должен быть абсолютным", src)
		}
		if _, err := os.Stat(src); err != nil {
			// Пропуск — только для умолчаний (/lib64 есть не везде). Свой rw
			// или cwd, которого нет, — ошибка: сервер молча остался бы без
			// каталога, за который его просили пустить.
			if optional {
				return nil
			}
			return fmt.Errorf("sandbox: путь %q не существует", src)
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
	// /tmp, и --tmpfs /tmp поверх него спрятал бы привязку. Размер у tmpfs
	// свой: без него сервер набивает /tmp до лимита хоста.
	if sb.TmpMb > 0 {
		args = append(args, "--size", fmt.Sprintf("%d", int64(sb.TmpMb)*1024*1024))
	}
	args = append(args, "--tmpfs", "/tmp", "--proc", "/proc", "--dev", "/dev")

	for _, p := range sb.RO {
		if err := bind("--ro-bind", p, slices.Contains(config.DefaultSandboxRO, p)); err != nil {
			return nil, err
		}
	}
	for _, p := range sb.RW {
		if err := bind("--bind", p, false); err != nil {
			return nil, err
		}
	}
	// Рабочая папка сервера: без бинда --chdir внутри песочницы некуда.
	if srv.Cwd != "" {
		if err := bind("--ro-bind", srv.Cwd, false); err != nil {
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
		// Абсолютный путь: bwrap делает execvp по PATH ребёнка, и без
		// /usr в ro падало «bwrap: execvp prlimit: No such file or directory»
		// без подсказки, что чинить.
		prlimit := "prlimit"
		if p, err := exec.LookPath("prlimit"); err == nil {
			prlimit = p
		}
		args = append(args, "--", prlimit)
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
	// Отдельный файл не должен быть больше самого tmpfs.
	if sb.TmpMb > 0 {
		out = append(out, fmt.Sprintf("--fsize=%d", int64(sb.TmpMb)*1024*1024))
	}
	return out
}

// Probe сообщает, доступна ли песочница на этом хосте: bwrap есть и userns
// разрешён. Тесты и doctor спрашивают то же самое, что проверяет Wrap.
func Probe() error { return available() }
