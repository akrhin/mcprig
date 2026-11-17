package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
)

// metaTimeout reads _meta.timeoutMs the way a client sees it on the wire.
func metaTimeout(t *testing.T, res *mcp.CallToolResult) int {
	t.Helper()
	raw, err := json.Marshal(res.Meta)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	var meta struct {
		TimeoutMS int `json:"timeoutMs"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	return meta.TimeoutMS
}

// A per-call timeoutMs replaces the set's requestTimeout, and _meta names the
// limit actually used: without it the agent compares a duration to a guess.
func TestInvokeCommandTimeoutMsApplied(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, &config.Config{RequestTimeout: 60000}, "a")
	cs := clientFor(t, m.Server())

	res := call(t, cs, "invoke_command", map[string]any{
		"server": "a", "command_name": "echo",
		"parameters": map[string]any{"text": "ok"},
		"timeoutMs":  1500,
	})
	if res.IsError {
		t.Fatalf("invoke_command: %s", resultText(t, res))
	}
	if got := metaTimeout(t, res); got != 1500 {
		t.Fatalf("_meta.timeoutMs = %d, want 1500", got)
	}

	res = call(t, cs, "invoke_command", map[string]any{
		"server": "a", "command_name": "echo",
		"parameters": map[string]any{"text": "ok"},
	})
	if got := metaTimeout(t, res); got != 60000 {
		t.Fatalf("_meta.timeoutMs = %d, want the set's requestTimeout 60000", got)
	}
}

// The limit cuts the call: a 1 s timeoutMs stops a 3 s downstream while the set
// allows 60 s.
func TestInvokeCommandTimeoutMsStopsCall(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, &config.Config{RequestTimeout: 60000}, "a")
	cs := clientFor(t, m.Server())

	start := time.Now()
	res := call(t, cs, "invoke_command", map[string]any{
		"server": "a", "command_name": "echo",
		"parameters": map[string]any{"text": "slow", "ms": 3000},
		"timeoutMs":  1000,
	})
	if elapsed := time.Since(start); elapsed >= 2500*time.Millisecond {
		t.Fatalf("вызов шёл %v при timeoutMs 1000 и requestTimeout 60000", elapsed)
	}
	if !res.IsError {
		t.Fatalf("таймаут должен прийти с isError: %s", resultText(t, res))
	}
	if text := resultText(t, res); !strings.Contains(text, "таймаут вызова 1000 мс") {
		t.Fatalf("текст таймаута не называет фактический предел: %q", text)
	}
}

// timeoutMs вне границ отвергается обоими инструментами до запуска.
func TestTimeoutMsOutOfRange(t *testing.T) {
	f := newFanTracker()
	m := fanManager(t, f, nil, "a")
	cs := clientFor(t, m.Server())

	for _, ms := range []int{999, 600001} {
		res := call(t, cs, "invoke_command", map[string]any{
			"server": "a", "command_name": "echo", "timeoutMs": ms,
		})
		if !res.IsError {
			t.Fatalf("invoke_command timeoutMs=%d принят", ms)
		}
		if text := resultText(t, res); !strings.Contains(text, "timeoutMs: допустимо от 1000 до 600000 мс") {
			t.Fatalf("invoke_command timeoutMs=%d: %q", ms, text)
		}

		res = call(t, cs, "invoke_many", map[string]any{"calls": []any{
			map[string]any{"server": "a", "command_name": "echo", "timeoutMs": ms},
		}})
		if !res.IsError {
			t.Fatalf("invoke_many timeoutMs=%d принят", ms)
		}
		if text := resultText(t, res); !strings.Contains(text, "calls[0]: timeoutMs: допустимо от 1000 до 600000 мс") {
			t.Fatalf("invoke_many timeoutMs=%d: %q", ms, text)
		}
	}
}

// Каждый элемент батча идёт под своим пределом: медленный падает по таймауту,
// быстрый успевает, и отказ остаётся данными, а не isError.
func TestInvokeManyTimeoutMsPerCall(t *testing.T) {
	f := newFanTracker()
	// Набор разрешает 800 мс, элемент — 1500: бюджет веера обязан подняться,
	// иначе вызов срезался бы раньше своего предела.
	m := fanManager(t, f, &config.Config{RequestTimeout: 800}, "a")
	cs := clientFor(t, m.Server())

	start := time.Now()
	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		map[string]any{
			"server": "a", "command_name": "echo", "timeoutMs": 1500,
			"parameters": map[string]any{"text": "slow", "ms": 3000},
		},
		map[string]any{
			"server": "a", "command_name": "echo", "timeoutMs": 1500,
			"parameters": map[string]any{"text": "fast", "ms": 1000},
		},
	}})
	if elapsed := time.Since(start); elapsed >= 2500*time.Millisecond {
		t.Fatalf("батч шёл %v при timeoutMs 1500", elapsed)
	}
	if res.IsError {
		t.Fatalf("частичный отказ должен остаться данными: %s", resultText(t, res))
	}
	out := manyAnswers(t, res)
	if len(out) != 2 {
		t.Fatalf("ответов %d, want 2", len(out))
	}
	if out[0].OK || !strings.Contains(out[0].Error, "таймаут вызова 1500 мс") {
		t.Fatalf("calls[0] должен истечь по своему пределу: %+v", out[0])
	}
	if !out[1].OK {
		t.Fatalf("calls[1] длился 1 с при пределе 1500 мс и обязан успеть: %+v", out[1])
	}
	if out[0].TimeoutMS != 1500 || out[1].TimeoutMS != 1500 {
		t.Fatalf("timeout_ms элементов = %d, %d, want 1500", out[0].TimeoutMS, out[1].TimeoutMS)
	}
	if got := metaTimeout(t, res); got != 1500 {
		t.Fatalf("_meta.timeoutMs = %d, want 1500", got)
	}
}

// Подсказка сервера: встроенная по имени, своя из конфига поверх неё, и ничего
// у незнакомого сервера.
func TestListServersHint(t *testing.T) {
	own := stdio("context7")
	own.Hint = "доки вендора: сначала resolve-library-id"
	var dials int32
	m := testManager(&config.Config{Servers: []*config.Server{
		stdio("serena"), own, stdio("acme"),
	}}, &dials, 0)
	defer m.Close()
	cs := clientFor(t, m.Server())

	text := resultText(t, call(t, cs, "list_servers", nil))
	out := listServersOut(t, cs)
	byName := map[string]serversOutServer{}
	for _, s := range out.Servers {
		byName[s.Name] = s
	}
	if got := byName["serena"].Hint; !strings.Contains(got, "activate_project") {
		t.Fatalf("встроенная подсказка serena: %q", got)
	}
	if got := byName["context7"].Hint; got != own.Hint {
		t.Fatalf("подсказка из конфига не переопределяет встроенную: %q", got)
	}
	// Незнакомый сервер без hint: поля нет вовсе, чтобы агент не читал пустую
	// строку как «решать нечего».
	var raw struct {
		Servers []map[string]any `json:"servers"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("list_servers: %v", err)
	}
	for _, s := range raw.Servers {
		if s["name"] != "acme" {
			continue
		}
		if hint, ok := s["hint"]; ok {
			t.Fatalf("у незнакомого сервера лишнее поле hint: %v", hint)
		}
		return
	}
	t.Fatal("acme нет в list_servers")
}
