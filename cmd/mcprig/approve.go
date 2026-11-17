package main

// mcprig approve — одобрение текущего дескриптора инструмента. Файл карантина
// принадлежит набору, но пишет его процесс набора, поэтому одобрение идёт
// через панель: она тот же файл читает и перезаписывает.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/akrhin/mcprig/internal/toollock"
)

// approveConfig — что нужно одобрению; в тестах подставляется целиком.
type approveConfig struct {
	Set   string
	All   bool
	Panel string
	panelCreds
}

func newApproveFlags() (*flag.FlagSet, *approveConfig) {
	c := &approveConfig{}
	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.StringVar(&c.Set, "set", "main", "имя набора, в котором одобряется инструмент")
	fs.BoolVar(&c.All, "all", false, "одобрить все изменившиеся инструменты набора")
	panelFlags(fs, &c.Panel, panelHelp, &c.panelCreds)
	return fs, c
}

// runApprove разбирает флаги сам, а не через parseFlags: у команды есть
// позиционные аргументы (server/tool), которые parseFlags отбивает как лишние.
// Так же разбирает свои аргументы runAuth.
func runApprove(args []string) error {
	fs, o := newApproveFlags()
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return flag.ErrHelp
		}
		return &usageError{err.Error()}
	}
	targets := fs.Args()
	switch {
	case o.All && len(targets) > 0:
		return &usageError{"--all и список инструментов несовместимы: выберите одно"}
	case !o.All && len(targets) == 0:
		return &usageError{"укажите server/tool или --all"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := strings.TrimRight(panelBaseURL(o.Panel), "/")
	creds := o.panelCreds
	client, err := newPanelClient(ctx, base, creds)
	if err != nil {
		return err
	}

	if o.All {
		approved, err := panelApprove(ctx, client, base, creds, o.Set, map[string]any{"all": true})
		if err != nil {
			return err
		}
		if len(approved) == 0 {
			fmt.Printf("набор %s: одобрять нечего — все инструменты совпадают с одобренными\n", o.Set)
			return nil
		}
		fmt.Printf("набор %s: одобрено %d\n", o.Set, len(approved))
		for _, key := range approved {
			fmt.Println("  " + key)
		}
		return nil
	}
	for _, target := range targets {
		server, tool := toollock.Split(target)
		if server == "" || tool == "" {
			return &usageError{fmt.Sprintf("%q: нужен вид server/tool", target)}
		}
		// Автора записи называет панель: CLI идёт тем же API, и поле by в
		// теле не принимается — иначе одобрение выдаёт себя за чужое.
		body := map[string]any{"server": server, "tool": tool}
		if _, err := panelApprove(ctx, client, base, creds, o.Set, body); err != nil {
			return err
		}
		fmt.Printf("набор %s: одобрен %s\n", o.Set, target)
	}
	return nil
}

// panelApprove отправляет одно одобрение и возвращает одобренные ключи.
func panelApprove(ctx context.Context, client *http.Client, base string, c panelCreds, set string, body any) ([]string, error) {
	path := "/api/sets/" + set + "/tools/approve"
	raw, err := panelPost(ctx, client, base, c, path, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Approved []string `json:"approved"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("панель %s: %s: %w", base, path, err)
	}
	return out.Approved, nil
}
