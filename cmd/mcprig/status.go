package main

// mcprig status — срез панели в терминале: здоровье и наборы с портом,
// инструментами, ключом и последним вызовом.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// statusConfig — всё, что нужно status; в тестах подставляется целиком.
type statusConfig struct {
	Panel   string
	Set     string
	JSON    bool
	Timeout time.Duration
	panelCreds
}

// statusSet — одна строка отчёта и один элемент -json.
type statusSet struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	State    string `json:"state"`
	Port     int    `json:"port"`
	Tools    int    `json:"tools"`
	Key      bool   `json:"key"`
	LastCall string `json:"lastCall,omitempty"`
	Error    string `json:"error,omitempty"`
}

// statusReport — панель целиком: /healthz и /api/state.
type statusReport struct {
	Panel  string      `json:"panel"`
	Health string      `json:"health"`
	Sets   []statusSet `json:"sets"`
}

// panelStateSet — поля /api/state, нужные status.
type panelStateSet struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Port    int        `json:"port"`
	Keys    []panelKey `json:"keys"`
	Runtime struct {
		Running bool   `json:"running"`
		Port    int    `json:"port"`
		Error   string `json:"error"`
	} `json:"runtime"`
}

// panelKey — ключ набора в /api/state: отозванный ключ остаётся в списке
// (registry.Key.RevokedAt), поэтому выданным считается только неотозванный.
type panelKey struct {
	RevokedAt string `json:"revokedAt"`
}

func newStatusFlags() (*flag.FlagSet, *statusConfig) {
	c := &statusConfig{}
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.StringVar(&c.Set, "set", "", "только один набор")
	fs.BoolVar(&c.JSON, "json", false, "машинный вывод")
	fs.DurationVar(&c.Timeout, "timeout", 10*time.Second, "бюджет запроса к панели")
	panelFlags(fs, &c.Panel, panelHelp, &c.panelCreds)
	return fs, c
}

func runStatus(args []string) error {
	fs, o := newStatusFlags()
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if o.Timeout <= 0 {
		return &usageError{"--timeout должен быть положительным"}
	}
	rep, err := collectStatus(o)
	if err != nil {
		return err
	}
	var out []byte
	if o.JSON {
		body, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		body = append(body, '\n')
		out = body
	} else {
		out = renderStatus(rep)
	}
	_, err = os.Stdout.Write(out)
	return err
}

func collectStatus(o *statusConfig) (statusReport, error) {
	base := strings.TrimRight(panelBaseURL(o.Panel), "/")
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()

	creds := o.panelCreds
	client, err := newPanelClient(ctx, base, creds)
	if err != nil {
		return statusReport{}, err
	}
	health, err := panelHealth(ctx, client, base, creds)
	if err != nil {
		return statusReport{}, err
	}
	rep := statusReport{Panel: base, Health: health, Sets: []statusSet{}}

	raw, err := panelGet(ctx, client, base, creds, "/api/state", "состояние")
	if err != nil {
		return statusReport{}, err
	}
	var state struct {
		Sets []panelStateSet `json:"sets"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return statusReport{}, fmt.Errorf("панель %s: /api/state: %w", base, err)
	}

	found := false
	for _, s := range state.Sets {
		if o.Set != "" && s.ID != o.Set {
			continue
		}
		found = true
		row := statusSet{ID: s.ID, Name: s.Name, Port: s.Port, Key: hasActiveKey(s.Keys), State: "stopped"}
		if row.Port == 0 {
			row.Port = s.Runtime.Port
		}
		if s.Runtime.Running {
			row.State = "running"
			row.Tools, row.LastCall = setUsage(ctx, client, base, creds, s.ID)
		}
		row.Error = s.Runtime.Error
		rep.Sets = append(rep.Sets, row)
	}
	if o.Set != "" && !found {
		return statusReport{}, fmt.Errorf("набор %q не найден в панели %s", o.Set, base)
	}
	return rep, nil
}

// hasActiveKey: действующий ключ — единственный неотозванный (registry.keys).
func hasActiveKey(keys []panelKey) bool {
	for _, k := range keys {
		if k.RevokedAt == "" {
			return true
		}
	}
	return false
}

// setUsage достаёт число инструментов и время последнего вызова из /api/calls:
// /api/state их не несёт, а панель показывает те же цифры. Лучше молчание,
// чем пустая строка: сбой этого запроса не делает status ошибочным.
func setUsage(ctx context.Context, client *http.Client, base string, creds panelCreds, set string) (int, string) {
	raw, code, err := panelDo(ctx, client, base, creds, "/api/calls?set="+url.QueryEscape(set)+"&limit=1")
	if err != nil || code != http.StatusOK {
		return 0, ""
	}
	var calls struct {
		Calls []struct {
			Time time.Time `json:"time"`
		} `json:"calls"`
		Schema *struct {
			Tools int `json:"tools"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(raw, &calls); err != nil {
		return 0, ""
	}
	tools := 0
	if calls.Schema != nil {
		tools = calls.Schema.Tools
	}
	last := ""
	if len(calls.Calls) > 0 {
		last = calls.Calls[0].Time.Format(time.RFC3339)
	}
	return tools, last
}

// panelHealth: /healthz публичный и у degraded отвечает 503 — это ответ, а не
// сбой, поэтому код 503 здесь допустим.
func panelHealth(ctx context.Context, client *http.Client, base string, creds panelCreds) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return "", err
	}
	panelHeaders(req, creds)
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("панель %s: %w", base, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusServiceUnavailable {
		return "", fmt.Errorf("панель %s: /healthz: HTTP %d", base, res.StatusCode)
	}
	var hz struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&hz); err != nil || hz.Status == "" {
		return "", fmt.Errorf("панель %s: /healthz: не JSON со status", base)
	}
	return hz.Status, nil
}

func renderStatus(rep statusReport) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "mcprig status, панель %s\n", rep.Panel)
	fmt.Fprintf(&b, "здоровье: %s\n\n", rep.Health)

	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "набор\tсостояние\tпорт\tинструментов\tключ\tпоследний вызов")
	for _, s := range rep.Sets {
		state := "остановлен"
		if s.State == "running" {
			state = "запущен"
		}
		if s.Error != "" {
			state += " (" + s.Error + ")"
		}
		tools := "—"
		if s.State == "running" {
			tools = strconv.Itoa(s.Tools)
		}
		key := "нет"
		if s.Key {
			key = "есть"
		}
		last := "—"
		if s.LastCall != "" {
			if t, err := time.Parse(time.RFC3339, s.LastCall); err == nil {
				last = t.Local().Format("2006-01-02 15:04:05")
			} else {
				last = s.LastCall
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", s.ID, state, s.Port, tools, key, last)
	}
	_ = tw.Flush()
	fmt.Fprintf(&b, "\nитог: %d %s\n", len(rep.Sets), pluralBy(len(rep.Sets), "набор", "набора", "наборов"))
	return []byte(b.String())
}
