package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/downstream"
	"github.com/akrhin/mcprig/internal/oauth"
)

// authTarget — то, что нужно подкоманде auth: конфиг набора, каталог его
// хранилища токенов и сам сервер.
type authTarget struct {
	Cfg      *config.Config
	CfgPath  string
	StoreDir string
	Server   *config.Server
}

// resolveAuth находит сервер по имени. -set берёт конфиг и хранилище из каталога
// набора, -config — тот же конфиг, но хранилище рядом с данными процесса: иначе
// токен лёг бы в чужое место и набор его не увидел.
func resolveAuth(setID, cfgPath, dataDir, name string) (*authTarget, error) {
	switch {
	case setID == "" && cfgPath == "":
		return nil, errors.New("укажите набор: mcprig auth -set <набор> <сервер> (или -config <servers.json>)")
	case setID != "" && cfgPath != "":
		return nil, errors.New("укажите что-то одно: -set или -config")
	}
	t := &authTarget{CfgPath: cfgPath, StoreDir: dataDir}
	if setID != "" {
		t.CfgPath = filepath.Join(dataDir, "sets", setID, "servers.json")
		t.StoreDir = filepath.Join(dataDir, "sets", setID)
	}
	cfg, err := config.Load(t.CfgPath)
	if err != nil {
		return nil, err
	}
	t.Cfg = cfg
	for _, srv := range cfg.Servers {
		if srv.Name == name {
			t.Server = srv
			break
		}
	}
	if t.Server == nil {
		return nil, fmt.Errorf("сервер %q не найден в %s — сверьте имя: mcprig doctor", name, t.CfgPath)
	}
	return t, nil
}

// humanWait — длительность ожидания для человека: «6 мин» вместо «6m0s».
func humanWait(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d с", int(d.Seconds()))
	}
	return fmt.Sprintf("%d мин", int(d.Minutes()))
}

// runAuth проводит OAuth-вход вне запроса клиента: health-проба набора ждёт
// ответ 15 с, а браузер — минуты, поэтому интерактивный поток живёт здесь.
func runAuth(args []string) error {
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	setID := fs.String("set", "", "имя набора: конфиг и хранилище токенов берутся из него")
	cfgPath := fs.String("config", "", "путь к файлу servers.json вместо -set")
	timeout := fs.Duration("timeout", 6*time.Minute, "сколько ждать входа в браузере")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return flag.ErrHelp
		}
		return &usageError{err.Error()}
	}
	if fs.NArg() != 1 {
		return &usageError{"нужно имя сервера: mcprig auth -set main <сервер>"}
	}

	t, err := resolveAuth(*setID, *cfgPath, stateDir(), fs.Arg(0))
	if err != nil {
		return err
	}
	// Ссылку печатаем сами, браузер не запускаем: команду зовут и с сервера без
	// графической сессии, где xdg-open всё равно бесполезен.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	store, err := oauth.OpenStore(t.StoreDir)
	if err != nil {
		return err
	}
	// Env пустой: это команда владельца, а не набор — stdio-сервер получает то
	// же окружение, с которым работал бы из терминала (HOME, прокси).
	conn := &downstream.Connector{
		Version:             version,
		OAuth:               t.Cfg.OAuth,
		Store:               store,
		Log:                 log,
		SingleAuthorization: true,
		Browser: func(url string) error {
			fmt.Printf("откройте ссылку в браузере:\n\n%s\n\n", url)
			return nil
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	fmt.Fprintf(os.Stderr, "авторизация %s (%s), ждём до %s\n", t.Server.Name, t.CfgPath, time.Now().Add(*timeout).Format("15:04:05"))
	sess, err := conn.Connect(ctx, t.Server)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("ссылка не использована за %s — токен не сохранён; повторите mcprig auth", humanWait(*timeout))
		}
		return fmt.Errorf("авторизация %s: %w", t.Server.Name, err)
	}
	defer sess.Close()

	if _, ok := store.Load(oauth.Key(t.Server.Name, t.Server.URL)); ok {
		fmt.Printf("токен сохранён: %s\n", filepath.Join(t.StoreDir, "oauth.json"))
		fmt.Println("перезапустите набор в панели: процесс читает токен при старте")
	} else {
		fmt.Printf("%s подключился без OAuth — токен не понадобился\n", t.Server.Name)
	}
	return nil
}
