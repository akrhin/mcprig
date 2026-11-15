package main

// mcprig logs — хвост лога набора или одного его сервера из панели, с
// опросом -f.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// logsPollInterval — шаг опроса -f; переменная, чтобы тест не ждал секунду.
var logsPollInterval = time.Second

// logsConfig — всё, что нужно logs; в тестах подставляется целиком.
type logsConfig struct {
	Panel    string
	Set      string
	Server   string
	Lines    int
	Follow   bool
	JSON     bool
	Password string
	Secret   string
	User     string
}

// logLine — строка лога: панель отдаёт строку либо объект {line, ts}.
type logLine struct {
	Line       string
	TS         string
	Structured bool
}

func newLogsFlags() (*flag.FlagSet, *logsConfig) {
	c := &logsConfig{}
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.StringVar(&c.Panel, "panel", "", "адрес панели (по умолчанию MCPRIG_PANEL_URL, иначе MCPRIG_PORT)")
	fs.StringVar(&c.Set, "set", "", "набор, чей лог читать (обязательно)")
	fs.StringVar(&c.Server, "server", "", "лог одного сервера набора, а не всего набора")
	fs.IntVar(&c.Lines, "n", 200, "сколько последних строк взять")
	fs.BoolVar(&c.Follow, "f", false, "следить: опрос раз в секунду, печатать только новые строки")
	fs.BoolVar(&c.JSON, "json", false, "машинный вывод: объект на строку")
	fs.StringVar(&c.Password, "password", "", "пароль панели (по умолчанию MCPRIG_PASSWORD)")
	fs.StringVar(&c.Secret, "secret", "", "секрет обратного прокси панели (по умолчанию MCPRIG_PROXY_SECRET)")
	fs.StringVar(&c.User, "user", "", "пользователь панели для входа (по умолчанию admin)")
	return fs, c
}

func runLogs(args []string) error {
	fs, o := newLogsFlags()
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	// Ctrl-C завершает -f штатно: снимаем контекст и выходим без ошибки.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runLogsContext(ctx, o)
}

func runLogsContext(ctx context.Context, o *logsConfig) error {
	if o.Set == "" {
		return &usageError{"укажите --set: имя набора"}
	}
	if o.Lines < 1 {
		return &usageError{"--n должен быть положительным"}
	}
	base := strings.TrimRight(panelBaseURL(o.Panel), "/")
	creds := panelCreds{Password: o.Password, Secret: o.Secret, User: o.User}
	client, err := newPanelClient(ctx, base, creds)
	if err != nil {
		return err
	}
	fetch := func(ctx context.Context) ([]logLine, error) {
		return fetchLogs(ctx, client, base, creds, o)
	}

	prev, err := fetch(ctx)
	if err != nil {
		// Ctrl-C во время первого запроса — такой же штатный выход, как в цикле.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	printLogs(o, prev)
	if !o.Follow {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(logsPollInterval):
		}
		cur, err := fetch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		printLogs(o, newLogLines(prev, cur))
		prev = cur
	}
}

func fetchLogs(ctx context.Context, client *http.Client, base string, creds panelCreds, o *logsConfig) ([]logLine, error) {
	path := "/api/sets/" + url.PathEscape(o.Set) + "/logs"
	if o.Server != "" {
		path = "/api/sets/" + url.PathEscape(o.Set) + "/servers/" + url.PathEscape(o.Server) + "/logs"
	}
	path += "?lines=" + strconv.Itoa(o.Lines)
	raw, err := panelGet(ctx, client, base, creds, path, "лог")
	if err != nil {
		return nil, err
	}
	var res struct {
		Logs []json.RawMessage `json:"logs"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("панель %s: %s: %w", base, path, err)
	}
	out := make([]logLine, 0, len(res.Logs))
	for _, r := range res.Logs {
		out = append(out, decodeLogLine(r))
	}
	return out, nil
}

func decodeLogLine(raw json.RawMessage) logLine {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return logLine{Line: s}
	}
	var obj struct {
		Line string `json:"line"`
		TS   string `json:"ts"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return logLine{Line: obj.Line, TS: obj.TS, Structured: true}
	}
	return logLine{Line: string(raw)}
}

// newLogLines — строки, которых не было в прошлом ответе: у хвоста панели нет
// курсора, поэтому общая часть ищется от длинной к короткой.
func newLogLines(prev, cur []logLine) []logLine {
	n := min(len(prev), len(cur))
	for k := n; k > 0; k-- {
		if !equalLines(prev[len(prev)-k:], cur[:k]) {
			continue
		}
		if k == len(cur) {
			// Весь ответ — хвост прежнего: то же самое (печатать нечего) или
			// повторяющийся лог, где «новое» от повтора неотличимо.
			if k == len(prev) {
				return nil
			}
			// shortcut: без курсора в ответе панели повтор целиком не
			// отличить от нового — печатаем весь ответ, дубли лучше тишины;
			// курсор в /api/.../logs уберёт и это.
			return cur
		}
		return cur[k:]
	}
	return cur
}

func equalLines(a, b []logLine) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func printLogs(o *logsConfig, lines []logLine) {
	if len(lines) == 0 {
		return
	}
	var b strings.Builder
	for _, l := range lines {
		if !o.JSON {
			b.WriteString(l.Line)
			b.WriteByte('\n')
			continue
		}
		var body []byte
		if l.Structured {
			// short: панель пока отдаёт строки; структура {line, ts} — на
			// будущее, тогда этот объект заполнится весь.
			body, _ = json.Marshal(struct {
				Set    string `json:"set"`
				Server string `json:"server"`
				Line   string `json:"line"`
				TS     string `json:"ts"`
			}{o.Set, o.Server, l.Line, l.TS})
		} else {
			body, _ = json.Marshal(struct {
				Line string `json:"line"`
			}{l.Line})
		}
		b.Write(body)
		b.WriteByte('\n')
	}
	if _, err := os.Stdout.WriteString(b.String()); err != nil {
		fmt.Fprintln(os.Stderr, "mcprig: logs:", err)
	}
}
