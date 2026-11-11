// Package sandbox wraps a stdio server's argv in bubblewrap: its own mount
// tree, read-only root, no /home, no network. Transport does not change —
// stdin/stdout still belong to the process, so mcp.CommandTransport works.
package sandbox

import (
	"bytes"
	"context"
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
	"time"

	"github.com/akrhin/mcprig/internal/config"
)

// available проверяет, что bwrap действительно запускается: бинарь есть и
// userns разрешён. Переменная, а не функция в вызове, — тесты подменяют.
var available = probe

// probeTimeout — сколько ждём пробный запуск: зависший bwrap не должен
// блокировать запуск серверов навсегда. Переменная — тесты сокращают ожидание.
var probeTimeout = 10 * time.Second

// probeState — одна проба на процесс: успех кэшируется, неудача — нет (ранняя
// гонка на /proc не должна навсегда запрещать песочницу), но пришедшие во
// время пробы ждут её результата и не запускают свою.
var probeState struct {
	mu      sync.Mutex
	ok      bool
	running bool
	done    chan struct{}
	err     error
}

func probe() error {
	probeState.mu.Lock()
	if probeState.ok {
		probeState.mu.Unlock()
		return nil
	}
	if probeState.running {
		done := probeState.done
		probeState.mu.Unlock()
		<-done
		probeState.mu.Lock()
		defer probeState.mu.Unlock()
		return probeState.err
	}
	probeState.running = true
	probeState.done = make(chan struct{})
	done := probeState.done
	probeState.mu.Unlock()

	err := probeRun()

	probeState.mu.Lock()
	probeState.running = false
	probeState.err = err
	probeState.ok = err == nil
	close(done)
	probeState.mu.Unlock()
	return err
}

func probeRun() error {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return errors.New("bwrap не найден в PATH")
	}
	if _, err := exec.LookPath("prlimit"); err != nil {
		return errors.New("prlimit не найден в PATH: лимиты песочницы задаёт util-linux — установите пакет util-linux")
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
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bwrap, "--unshare-all", "--die-with-parent",
		"--ro-bind-try", "/usr", "/usr",
		"--ro-bind-try", "/bin", "/bin",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--", trueBin)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("bwrap не ответил за %s: проба песочницы прервана", probeTimeout)
		}
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
	// prlimit — динамический бинарник: внутри пустого корня ему нужны и сам
	// файл, и загрузчик с libc, иначе «bwrap: execvp /usr/bin/prlimit: No
	// such file or directory» — даже когда /usr есть в ro, но /lib64 нет.
	// Лимиты при этом остаются частью песочницы: prlimit внутри userns.
	limits := limitArgs(sb)
	var prlimit string
	if len(limits) > 0 {
		var err error
		if prlimit, err = exec.LookPath("prlimit"); err != nil {
			return nil, errors.New("sandbox: prlimit не найден в PATH (пакет util-linux)")
		}
		if err := bind("--ro-bind", prlimit, false); err != nil {
			return nil, err
		}
		for _, p := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"} {
			if err := bind("--ro-bind", p, true); err != nil {
				return nil, err
			}
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
	if len(limits) > 0 {
		// Абсолютный путь: bwrap делает execvp по PATH ребёнка, и по имени
		// он искал prlimit в PATH песочницы, куда его никто не клал.
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
