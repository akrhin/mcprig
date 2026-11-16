// Command mcprig serves one set of MCP servers behind a single lazy proxy.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // G108: обработчики регистрируются всегда, но слушает их только MCPRIG_PPROF
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/downstream"
	"github.com/akrhin/mcprig/internal/httplisten"
	"github.com/akrhin/mcprig/internal/manager"
	"github.com/akrhin/mcprig/internal/mcpserver"
	"github.com/akrhin/mcprig/internal/oauth"
	"github.com/akrhin/mcprig/internal/stats"
	"github.com/akrhin/mcprig/internal/toolout"
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
	// Свои таймауты: у pprof-сервера нет ни WriteTimeout (профиль снимается
	// десятками секунд), ни бюджета заголовков по умолчанию.
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			fmt.Fprintln(os.Stderr, "mcprig: pprof:", err)
		}
	}()
	fmt.Fprintln(os.Stderr, "mcprig: pprof на", addr)
}

func main() {
	// Скрытый режим: набор запускает сервер сторожем, который переживает
	// SIGKILL набора и гасит свою группу (см. downstream.GuardMain). Разбор
	// флагов сюда не доходит: у режима собственная argv.
	downstream.RunChildGuardMode(os.Args)
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
	var ue *usageError
	if errors.As(err, &ue) {
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
	cfgPath = fs.String("config", defaultConfigPath(), "путь к файлу servers.json")
	showVersion = fs.Bool("version", false, "показать версию и выйти")
	addr = fs.String("addr", "", "адрес прослушивания, по умолчанию берётся из поля transport конфигурации")
	return fs, cfgPath, showVersion, addr
}

func newStdioFlags() (fs *flag.FlagSet, cfgPath *string) {
	fs = flag.NewFlagSet("stdio", flag.ContinueOnError)
	cfgPath = fs.String("config", defaultConfigPath(), "путь к файлу servers.json")
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
		return &usageError{fmt.Sprintf("лишние аргументы: %s; команда принимает: serve|stdio|auth|approve|panel|init|doctor|warm|status|logs", strings.Join(fs.Args(), ", "))}
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
	case "auth":
		// Позиционный аргумент — имя сервера, поэтому разбор внутри runAuth.
		return "auth", args[1:], nil
	case "approve":
		// Позиционные server/tool, поэтому разбор внутри runApprove.
		return "approve", args[1:], nil
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
	case "status":
		fs, _ := newStatusFlags()
		if err := parseFlags(fs, args[1:]); err != nil {
			return "", nil, err
		}
		return "status", args[1:], nil
	case "logs":
		fs, _ := newLogsFlags()
		if err := parseFlags(fs, args[1:]); err != nil {
			return "", nil, err
		}
		return "logs", args[1:], nil
	case "panel":
		if len(args) > 1 {
			return "", nil, &usageError{fmt.Sprintf("panel не принимает аргументов: %q", args[1:])}
		}
		return "panel", nil, nil
	}
	return "", nil, &usageError{fmt.Sprintf("неизвестная подкоманда %q; доступны: serve, stdio, auth, approve, panel, init, doctor, warm, status, logs", args[0])}
}

// tooloutInterval is a variable only so a test can shorten the sweep; serve and
// stdio both use it.
var tooloutInterval = toolout.Interval

// startToolout sweeps the tool-out directory now and then periodically until ctx
// is done. serve and stdio share one base directory — the same one the proxy
// writes truncated answers into — so stdio's files are cleaned too.
func startToolout(ctx context.Context, log *slog.Logger) {
	cfg, warns := toolout.FromEnv()
	for _, w := range warns {
		log.Warn("tool-out: " + w)
	}
	toolout.Run(ctx, toolout.Base(), cfg, tooloutInterval, log)
}

func run() error {
	mode, rest, err := parseArgs(os.Args[1:])
	if err != nil {
		return err
	}
	switch mode {
	case "stdio":
		return runStdio(rest)
	case "auth":
		return runAuth(rest)
	case "panel":
		return manager.Run(manager.FromEnv(version))
	case "init":
		return runInit(rest)
	case "doctor":
		return runDoctor(rest)
	case "warm":
		return runWarm(rest)
	case "approve":
		return runApprove(rest)
	case "status":
		return runStatus(rest)
	case "logs":
		return runLogs(rest)
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
	stopTracing := startTracing(log)
	defer stopTracing()
	store, err := oauth.OpenStore(stateDir())
	if err != nil {
		return err
	}
	// Env: ребёнку уходит whitelist плюс envAllow сервера, а не весь os.Environ().
	conn := &downstream.Connector{Version: version, OAuth: cfg.OAuth, Store: store, Log: log, Env: downstream.EnvFor}
	mgr := mcpserver.New(cfg, version, conn)
	defer func() {
		if err := mgr.Close(); err != nil {
			log.Warn("закрытие менеджера", "err", err)
		}
	}()
	mgr.LockPath = lockPath()

	rec := stats.NewRecorder(os.Getenv("MCPRIG_SET"), 0)
	// Recorder до прогрева: прогрев наполняет кэш инструментов и публикует
	// вес схем, иначе панель видит ноль до первого list_commands.
	mgr.Stats = rec

	// История рядом с конфигом: у набора это sets/<id>/, у standalone serve —
	// каталог, где лежит сам servers.json. Без неё прокси работает дальше.
	if hist, err := stats.OpenHistory(filepath.Join(filepath.Dir(*cfgPath), "stats"), log); err != nil {
		log.Warn("stats: история на диске недоступна", "err", err)
	} else {
		rec.SetHistory(hist)
		defer hist.Close()
	}

	// Прогрев в фоне: иначе первый list_commands ждёт, пока поднимутся все
	// npx/uvx-серверы (на одиннадцати это ~11 с).
	go mgr.Warm(context.Background())
	// Маркер усечения называет /tool-out, только когда файл можно забрать: у
	// открытого набора ключа нет, и подсказка вела бы в 401.
	mgr.ToolOutURL = len(transportTokens(cfg)) > 0
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

	// SIGHUP перечитывает servers.json на живом процессе: супервизор правит
	// один сервер и не рвёт набор целиком.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go reloadLoop(ctx, hup, mgr, handler, *cfgPath, log)

	// Выгрузки усечённых ответов чистятся при старте и дальше периодически.
	startToolout(ctx, log)

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

// reloadLoop перечитывает servers.json по SIGHUP, пока процесс жив.
func reloadLoop(ctx context.Context, hup <-chan os.Signal, mgr *mcpserver.Manager, handler *httplisten.Server, cfgPath string, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
		}
		res := reloadOnce(mgr, handler, cfgPath, log)
		added, updated, removed, reset := countApplied(res.Applied)
		log.Info(fmt.Sprintf("reload: применено %d, изменено %d, удалено %d, generation %d",
			len(res.Applied), updated+reset, removed, res.Generation),
			"added", added, "updated", updated, "reset", reset, "removed", removed,
			"restartNeeded", res.RestartNeeded, "err", res.Err)
	}
}

// reloadOnce делает одну попытку и пишет ack супервизору. Битый конфиг не
// роняет набор: прежний конфиг продолжает работать, ошибка уходит в status.
func reloadOnce(mgr *mcpserver.Manager, handler *httplisten.Server, cfgPath string, log *slog.Logger) mcpserver.ReloadResult {
	dir := filepath.Dir(cfgPath)
	// Метку читаем до работы: пока грузится конфиг, супервизор может начать
	// следующую попытку, и её nonce не должен достаться нашему ответу.
	nonce := readReloadNonce(dir)
	cfg, err := config.Load(cfgPath)
	var res mcpserver.ReloadResult
	if err != nil {
		res = mgr.ReloadFailed(err)
	} else {
		// Политика доступа применяется слушателем здесь же: выдача нового
		// ключа и потеря последнего не ждут перезапуска набора. Остальной
		// транспорт — дело mgr.Reload.
		if handler != nil && cfg.Transport != nil {
			handler.SetAuth(transportTokens(cfg), cfg.Transport.AllowAnonymous, cfg.Transport.ClientNames)
		}
		res = mgr.Reload(cfg)
	}
	if werr := writeReloadStatus(filepath.Join(dir, "reload.status"), res, nonce); werr != nil {
		log.Warn("reload.status не записан", "err", werr)
	}
	return res
}

// transportTokens — токены, которые принимает слушатель: список ключей, а у
// файла с одним ключом — сам authToken.
func transportTokens(cfg *config.Config) []string {
	if cfg == nil || cfg.Transport == nil {
		return nil
	}
	if len(cfg.Transport.AuthTokens) > 0 {
		return cfg.Transport.AuthTokens
	}
	if cfg.Transport.AuthToken != "" {
		return []string{cfg.Transport.AuthToken}
	}
	return nil
}

// readReloadNonce берёт метку попытки, которую супервизор положил рядом с
// конфигом до SIGHUP: ack несёт её, и ответ прошлой попытки не сойдёт за новый.
func readReloadNonce(dir string) string {
	body, err := os.ReadFile(filepath.Join(dir, "reload.request"))
	if err != nil {
		return ""
	}
	var req struct {
		Nonce string `json:"nonce"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	return req.Nonce
}

// countApplied считает записи вида "added:name".
func countApplied(applied []string) (added, updated, removed, reset int) {
	for _, item := range applied {
		switch {
		case strings.HasPrefix(item, "added:"):
			added++
		case strings.HasPrefix(item, "updated:"):
			updated++
		case strings.HasPrefix(item, "removed:"):
			removed++
		case strings.HasPrefix(item, "reset:"):
			reset++
		}
	}
	return added, updated, removed, reset
}

// reloadStatus — единственный ack для супервизора: HTTP для этого не заводим.
type reloadStatus struct {
	// Nonce повторяет reload.request: ответ относится ровно к этой попытке.
	Nonce         string   `json:"nonce,omitempty"`
	Generation    int      `json:"generation"`
	At            string   `json:"at"`
	Applied       []string `json:"applied"`
	RestartNeeded []string `json:"restartNeeded"`
	Error         string   `json:"error"`
}

// writeReloadStatus пишет status атомарно (временный файл + rename), чтобы
// супервизор не прочитал половину: старый файл остаётся до удачного rename.
func writeReloadStatus(path string, res mcpserver.ReloadResult, nonce string) error {
	st := reloadStatus{
		Nonce:         nonce,
		Generation:    res.Generation,
		At:            time.Now().Format(time.RFC3339),
		Applied:       res.Applied,
		RestartNeeded: res.RestartNeeded,
		Error:         res.Err,
	}
	if st.Applied == nil {
		st.Applied = []string{}
	}
	if st.RestartNeeded == nil {
		st.RestartNeeded = []string{}
	}
	body, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".reload.status-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close() // первична ошибка Chmod
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close() // первична ошибка Write
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "mcprig", "servers.json")
	}
	return "servers.json"
}

// stdioClient — метка клиента в истории stdio-режима: клиент у процесса один.
const stdioClient = "stdio"

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
	stopTracing := startTracing(log)
	defer stopTracing()
	store, err := oauth.OpenStore(stateDir())
	if err != nil {
		return err
	}
	// Env: ребёнку уходит whitelist плюс envAllow сервера, а не весь os.Environ().
	conn := &downstream.Connector{Version: version, OAuth: cfg.OAuth, Store: store, Log: log, Env: downstream.EnvFor}
	mgr := mcpserver.New(cfg, version, conn)
	defer func() {
		if err := mgr.Close(); err != nil {
			log.Warn("закрытие менеджера", "err", err)
		}
	}()
	mgr.LockPath = lockPath()

	// История вызовов: у stdio нет панели над процессом, но разрез по
	// периодам читается с диска так же, как у набора. Без неё прокси работает
	// дальше — клиенту важнее ответ, чем учёт.
	rec := stats.NewRecorder(os.Getenv("MCPRIG_SET"), 0)
	rec.SetClientLabel(stdioClient)
	if hist, err := stats.OpenHistory(filepath.Join(stateDir(), "stdio", "stats"), log); err != nil {
		log.Warn("stats: история на диске недоступна", "err", err)
	} else {
		rec.SetHistory(hist)
		defer hist.Close()
	}
	mgr.Stats = rec

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Выгрузки усечённых ответов копятся в том же базовом каталоге, что у
	// serve, и чистить их должен сам процесс: stdio-набор живёт без панели.
	startToolout(ctx, log)

	return mgr.Server().Run(ctx, &mcp.StdioTransport{})
}

// startTracing включает OTLP-экспорт, если он задан окружением. Возвращённая
// функция гасит экспортер и дописывает накопленный батч: иначе последние спаны
// теряются вместе с процессом.
func startTracing(log *slog.Logger) func() {
	shutdown, err := mcpserver.SetupTracing(context.Background())
	if err != nil {
		log.Warn("трассировка: экспортер не поднялся, спаны не уходят", "err", err)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			log.Warn("трассировка: остановка экспортера", "err", err)
		}
	}
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

// lockPath names the approved-descriptor record. A set process gets its own
// directory in MCPRIG_DATA_DIR (internal/manager/runtime.go, setEnv), so the
// file sits right there — the same path the panel reads and approves. A manual
// serve without a set has nobody to approve, and the quarantine stays off.
func lockPath() string {
	if dir := os.Getenv("MCPRIG_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "tools.lock.json")
	}
	return ""
}
