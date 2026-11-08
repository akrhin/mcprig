// Command mcp-manager serves one set of MCP servers behind a single lazy proxy.
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
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sintez/mcp-manager/internal/config"
	"github.com/sintez/mcp-manager/internal/downstream"
	"github.com/sintez/mcp-manager/internal/httplisten"
	"github.com/sintez/mcp-manager/internal/mcpserver"
	"github.com/sintez/mcp-manager/internal/oauth"
)

// version is set by the release build.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-manager:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "stdio":
			return runStdio(args[1:])
		case "serve":
			args = args[1:]
		}
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "путь к servers.json")
	showVersion := fs.Bool("version", false, "показать версию и выйти")
	addr := fs.String("addr", "", "адрес прослушивания, по умолчанию берётся из transport")
	_ = fs.Parse(args)

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if cfg.Transport == nil || cfg.Transport.AuthToken == "" {
		return errors.New("в конфиге нет transport.authToken: прокси не стартует без ключа")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store, err := oauth.OpenStore(stateDir())
	if err != nil {
		return err
	}
	conn := &downstream.Connector{Version: version, OAuth: cfg.OAuth, Store: store, Log: log}
	mgr := mcpserver.New(cfg, version, conn)
	defer mgr.Close()

	handler, err := httplisten.New(cfg, mgr.Server(), log)
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

	srv := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		return filepath.Join(dir, "lazy-mcp", "servers.json")
	}
	return "servers.json"
}

// runStdio serves one set over stdin/stdout: the client spawns us and owns the
// process. No listener, no key — the transport is the boundary.
func runStdio(args []string) error {
	fs := flag.NewFlagSet("stdio", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "путь к servers.json")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store, err := oauth.OpenStore(stateDir())
	if err != nil {
		return err
	}
	conn := &downstream.Connector{Version: version, OAuth: cfg.OAuth, Store: store, Log: log}
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
