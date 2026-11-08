package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
)

// initFlags — генератор клиентской настройки. Печатает фрагмент, который
// человек вставляет в свой конфиг сам: чужие файлы не переписываем.
type initConfig struct {
	Client string
	Set    string
	Remote string
	Key    string
	Binary string
	Config string
}

func newInitFlags() (*flag.FlagSet, *initConfig) {
	cfg := &initConfig{}
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.StringVar(&cfg.Client, "client", "opencode", "клиент: opencode, claude, codex")
	fs.StringVar(&cfg.Set, "set", "demo", "имя набора")
	fs.StringVar(&cfg.Remote, "remote", "", "адрес набора, напр. http://127.0.0.1:9100/mcp; пусто — stdio")
	fs.StringVar(&cfg.Key, "key", "", "bearer-ключ набора (только для --remote)")
	fs.StringVar(&cfg.Binary, "binary", "mcprig", "путь к бинарю mcprig для stdio-режима")
	fs.StringVar(&cfg.Config, "config", "", "путь к servers.json для stdio-режима")
	return fs, cfg
}

func runInit(args []string) error {
	fs, cfg := newInitFlags()
	if err := fs.Parse(args); err != nil {
		return &usageError{err.Error()}
	}
	if fs.NArg() > 0 {
		return &usageError{fmt.Sprintf("лишние аргументы: %q", fs.Args())}
	}
	client := strings.ToLower(cfg.Client)
	switch client {
	case "opencode", "claude", "codex":
	default:
		return &usageError{fmt.Sprintf("неизвестный клиент %q; доступны: opencode, claude, codex", cfg.Client)}
	}
	if cfg.Remote != "" && cfg.Key == "" {
		return &usageError{"для --remote нужен --key (ключ набора выдаёт панель)"}
	}
	if cfg.Remote == "" && cfg.Config == "" {
		return &usageError{"для stdio-режима нужен --config (путь к servers.json набора)"}
	}

	name := "mcprig-" + cfg.Set
	stdioArgs := []string{cfg.Binary, "stdio", "--config", cfg.Config}

	var out string
	switch client {
	case "opencode":
		entry := map[string]any{"type": "local", "command": stdioArgs, "enabled": true}
		if cfg.Remote != "" {
			entry = map[string]any{
				"type":    "remote",
				"url":     cfg.Remote,
				"enabled": true,
				"headers": map[string]string{"Authorization": "Bearer " + cfg.Key},
			}
		}
		out = marshal(map[string]any{"mcp": map[string]any{name: entry}})
	case "claude":
		entry := map[string]any{"command": cfg.Binary, "args": stdioArgs[1:]}
		if cfg.Remote != "" {
			entry = map[string]any{
				"type": "http",
				"url":  cfg.Remote,
				"headers": map[string]string{
					"Authorization": "Bearer " + cfg.Key,
				},
			}
		}
		out = marshal(map[string]any{"mcpServers": map[string]any{name: entry}})
	case "codex":
		// codex читает TOML; печатаем его, а не JSON.
		var b strings.Builder
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", name)
		if cfg.Remote != "" {
			fmt.Fprintf(&b, "url = %q\n", cfg.Remote)
			fmt.Fprintf(&b, "http_headers = { Authorization = %q }\n", "Bearer "+cfg.Key)
		} else {
			fmt.Fprintf(&b, "command = %q\n", cfg.Binary)
			fmt.Fprintf(&b, "args = [%q, %q, %q]\n", "stdio", "--config", cfg.Config)
		}
		out = b.String()
	}
	fmt.Println(out)
	return nil
}

func marshal(v any) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}
