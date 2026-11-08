// Command mcp-manager serves one set of MCP servers behind a single lazy proxy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sintez/mcp-manager/internal/config"
	"github.com/sintez/mcp-manager/internal/httplisten"
	"github.com/sintez/mcp-manager/internal/mcpserver"
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
	cfgPath := flag.String("config", defaultConfigPath(), "путь к servers.json")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	addr := flag.String("addr", "", "адрес прослушивания, по умолчанию берётся из transport")
	flag.Parse()

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
	mgr := mcpserver.New(cfg, version)
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
