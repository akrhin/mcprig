// Command mcprig serves one set of MCP servers behind a single lazy proxy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/downstream"
	"github.com/sintez/mcprig/internal/httplisten"
	"github.com/sintez/mcprig/internal/manager"
	"github.com/sintez/mcprig/internal/mcpserver"
	"github.com/sintez/mcprig/internal/oauth"
	"github.com/sintez/mcprig/internal/stats"
	"github.com/sintez/mcprig/internal/toolout"
)

// version is set by the release build.
var version = "dev"

// startPprof слушает MCPRIG_PPROF (например 127.0.0.1:6060), когда он задан:
// профиль нужен на живой нагрузке, а не только в бенчмарках.
func startPprof() {
	addr := os.Getenv("MCPRIG_PPROF")
	if addr == "" {
		return
	}
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Fprintln(os.Stderr, "mcprig: pprof:", err)
		}
	}()
	fmt.Fprintln(os.Stderr, "mcprig: pprof на", addr)
}

func main() {
	startPprof()
	if err := run(); err != nil {
		// -h: flag уже напечатал usage, это не ошибка.
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		// doctor напечатал отчёт сам; лишняя строка об ошибке только шумит.
		if errors.Is(err, errDoctorProblems) || errors.Is(err, errWarmProblems) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "mcprig:", err)
		os.Exit(exitCode(err))
	}
}

// exitCode: 0 — help, 2 — ошибка командной строки, 1 — всё прочее.
func exitCode(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if _, ok := err.(*usageError); ok {
		return 2
	}
	return 1
}

// usageError — ошибка командной строки: код возврата 2, как у пакета flag.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// newServeFlags описывает флаги serve один раз: их разбирает и parseArgs, и run.
func newServeFlags() (fs *flag.FlagSet, cfgPath *string, showVersion *bool, addr *string) {
	fs = flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath = fs.String("config", defaultConfigPath(), "путь к servers.json")
	showVersion = fs.Bool("version", false, "показать версию и выйти")
	addr = fs.String("addr", "", "адрес прослушивания, по умолчанию берётся из transport")
	return fs, cfgPath, showVersion, addr
}

func newStdioFlags() (fs *flag.FlagSet, cfgPath *string) {
	fs = flag.NewFlagSet("stdio", flag.ContinueOnError)
	cfgPath = fs.String("config", defaultConfigPath(), "путь к servers.json")
	return fs, cfgPath
}

// parseFlags разбирает флаги подкоманды. -h/-help — ErrHelp как есть:
// usage уже напечатан flag, main выйдет с кодом 0.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return flag.ErrHelp
		}
		return &usageError{err.Error()}
	}
	if fs.NArg() > 0 {
		return &usageError{fmt.Sprintf("лишние аргументы: %q", fs.Args())}
	}
	return nil
}

// parseArgs отделяет подкоманду от её флагов и отбивает лишние позиционные
// аргументы. Неизвестная подкоманда — ошибка, а не молчаливый serve: иначе
// опечатка поднимает прокси с конфигом по умолчанию.
func parseArgs(args []string) (mode string, rest []string, err error) {
	if len(args) == 0 {
		return "serve", nil, nil
	}
	// Флаги без подкоманды — это serve: так исторически запускают прокси.
	if strings.HasPrefix(args[0], "-") {
		fs, _, _, _ := newServeFlags()
		if err := parseFlags(fs, args); err != nil {
			return "", nil, err
		}
		return "serve", args, nil
	}
	switch args[0] {
	case "serve":
		fs, _, _, _ := newServeFlags()
		if err := parseFlags(fs, args[1:]); err != nil {
			return "", nil, err
		}
		return "serve", args[1:], nil
	case "stdio":
		fs, _ := newStdioFlags()
		if err := parseFlags(fs, args[1:]); err != nil {
			return "", nil, err
		}
		return "stdio", args[1:], nil
	case "init":
		if len(args) > 1 {
			return "init", args[1:], nil
		}
		return "init", nil, nil
	case "doctor":
		fs, _, _ := newDoctorFlags()
		if err := parseFlags(fs, args[1:]); err != nil {
			return "", nil, err
		}
		return "doctor", args[1:], nil
	case "warm":
		fs, _ := newWarmFlags()
		if err := parseFlags(fs, args[1:]); err != nil {
			return "", nil, err
		}
		return "warm", args[1:], nil
	case "panel":
		if len(args) > 1 {
			return "", nil, &usageError{fmt.Sprintf("panel не принимает аргументов: %q", args[1:])}
		}
		return "panel", nil, nil
	}
	return "", nil, &usageError{fmt.Sprintf("неизвестная подкоманда %q; доступны: serve, stdio, panel, init, doctor, warm", args[0])}
}

func run() error {
	mode, rest, err := parseArgs(os.Args[1:])
	if err != nil {
		return err
	}
	switch mode {
	case "stdio":
		return runStdio(rest)
	case "panel":
		return manager.Run(manager.FromEnv(version))
	case "init":
		return runInit(rest)
	case "doctor":
		return runDoctor(rest)
	case "warm":
		return runWarm(rest)
	}
	args := rest

	fs, cfgPath, showVersion, addr := newServeFlags()
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store, err := oauth.OpenStore(stateDir())
	if err != nil {
		return err
	}
	// Env: ребёнку уходит whitelist плюс envAllow сервера, а не весь os.Environ().
	conn := &downstream.Connector{Version: version, OAuth: cfg.OAuth, Store: store, Log: log, Env: downstream.EnvFor}
	mgr := mcpserver.New(cfg, version, conn)
	defer mgr.Close()

	rec := stats.NewRecorder(os.Getenv("MCPRIG_SET"), 0)
	// Recorder до прогрева: прогрев наполняет кэш инструментов и публикует
	// вес схем, иначе панель видит ноль до первого list_commands.
	mgr.Stats = rec

	// Прогрев в фоне: иначе первый list_commands ждёт, пока поднимутся все
	// npx/uvx-серверы (на одиннадцати это ~11 с).
	go mgr.Warm(context.Background())
	handler, err := httplisten.New(cfg, mgr.Server(), log, rec)
	if err != nil {
		return err
	}

	listen := *addr
	if listen == "" {
		host := cfg.Transport.Host
		if host == "" {
			host = "127.0.0.1"
		}
		port := cfg.Transport.Port
		if port == 0 {
			port = 9100
		}
		listen = net.JoinHostPort(host, fmt.Sprint(port))
	}

	// ReadTimeout не ставим: SSE-поток набора нельзя рвать по времени.
	srv := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Выгрузки усечённых ответов чистятся при старте и дальше раз в час.
	toolCfg, warns := toolout.FromEnv()
	for _, w := range warns {
		log.Warn("tool-out: " + w)
	}
	toolout.Run(ctx, toolout.Base(), toolCfg, toolout.Interval, log)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("прокси слушает", "addr", listen, "servers", len(cfg.Usable()))

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "mcprig", "servers.json")
	}
	return "servers.json"
}

// runStdio serves one set over stdin/stdout: the client spawns us and owns the
// process. No listener, no key — the transport is the boundary.
func runStdio(args []string) error {
	fs, cfgPath := newStdioFlags()
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store, err := oauth.OpenStore(stateDir())
	if err != nil {
		return err
	}
	// Env: ребёнку уходит whitelist плюс envAllow сервера, а не весь os.Environ().
	conn := &downstream.Connector{Version: version, OAuth: cfg.OAuth, Store: store, Log: log, Env: downstream.EnvFor}
	mgr := mcpserver.New(cfg, version, conn)
	defer mgr.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return mgr.Server().Run(ctx, &mcp.StdioTransport{})
}

// stateDir holds what must survive a restart but is not configuration: OAuth
// tokens now, the manager's registry later.
func stateDir() string {
	if dir := os.Getenv("MCPRIG_DATA_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "mcprig")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".mcprig"
	}
	return filepath.Join(home, ".local", "state", "mcprig")
}
