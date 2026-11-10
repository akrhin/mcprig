package sandbox_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/downstream"
	"github.com/akrhin/mcprig/internal/sandbox"
)

// probeResult — что удалось внутри песочницы. Песочница считается рабочей,
// только если всё «denied», а сами initialize/tools/list при этом живы.
type probeResult struct {
	Home     string `json:"home"`     // visible | hidden
	SSH      string `json:"ssh"`      // read | denied
	Net      string `json:"net"`      // reachable | blocked
	Write    string `json:"write"`    // written | denied
	ProcRoot string `json:"procRoot"` // readable | denied
	Canary   string `json:"canary"`   // leaked | clean
	Pid      string `json:"pid"`
}

const canaryVar = "MCPRIG_SANDBOX_CANARY"

// TestSandboxChildServer — тот же бинарь, запущенный под bwrap: настоящий
// stdio MCP-сервер, который пробует выйти из песочницы.
func TestSandboxChildServer(t *testing.T) {
	if os.Getenv("SANDBOX_CHILD_TEST") != "1" {
		t.Skip("служебный процесс: запускается из TestSandboxIsolatesStdioServer")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "probe", Description: "Try to escape the sandbox."},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			body, _ := json.Marshal(probe())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, nil, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		t.Fatalf("probe server: %v", err)
	}
}

func probe() probeResult {
	home, _ := os.UserHomeDir()
	out := probeResult{Home: "hidden", SSH: "denied", Net: "blocked", Write: "denied",
		ProcRoot: "denied", Canary: "clean", Pid: strconv.Itoa(os.Getpid())}
	if _, err := os.Stat(home); err == nil {
		out.Home = "visible"
	}
	// Файл есть на хосте, значит отказ — заслуга песочницы, а не отсутствия.
	if body, err := os.ReadFile(filepath.Join(home, ".ssh", "id_ed25519")); err == nil && len(body) > 0 {
		out.SSH = "read"
	}
	// Запись в домашний каталог: на хосте он writable, внутри не должен быть виден.
	if err := os.WriteFile(filepath.Join(home, "mcprig-pwned"), []byte("x"), 0o644); err == nil {
		out.Write = "written"
	}
	conn, err := net.DialTimeout("tcp", "1.1.1.1:443", time.Second)
	if err == nil {
		conn.Close()
		out.Net = "reachable"
	}
	if body, err := os.ReadFile("/proc/1/root/etc/shadow"); err == nil && len(body) > 0 {
		out.ProcRoot = "readable"
	}
	if os.Getenv(canaryVar) != "" {
		out.Canary = "leaked"
	}
	return out
}

func TestSandboxIsolatesStdioServer(t *testing.T) {
	// Не skip при первой же ошибке: тест обязан реально идти на этом хосте,
	// а причина пропуска должна быть видна.
	if err := sandbox.Probe(); err != nil {
		t.Skipf("bwrap недоступен: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(canaryVar, "1")

	srv := &config.Server{
		Name:      "probe",
		Transport: config.TransportStdio,
		Command:   []string{exe, "-test.run=TestSandboxChildServer"},
		Env:       map[string]string{"SANDBOX_CHILD_TEST": "1"},
		Sandbox: &config.Sandbox{
			Mode:          config.SandboxModeBwrap,
			OnUnavailable: config.OnUnavailableDeny,
			Network:       config.SandboxNetworkNone,
			// RLIMIT_AS — виртуальная память: Go-рантайм резервирует
			// адресное пространство заранее, 1 ГиБ ему мало.
			MemoryMb:   4096,
			TmpMb:      128,
			Pids:       128,
			TimeoutSec: 20,
			// Тестовый бинарь живёт в /tmp/go-buildNNN — иначе его не видно.
			RO: append(append([]string{}, config.DefaultSandboxRO...), filepath.Dir(exe)),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	session, err := (&downstream.Connector{Version: "test", Env: downstream.EnvFor}).Connect(ctx, srv)
	if err != nil {
		t.Fatalf("initialize под песочницей: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list под песочницей: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "probe" {
		t.Fatalf("неожиданный список инструментов: %+v", tools.Tools)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "probe"})
	if err != nil {
		t.Fatalf("вызов probe: %v", err)
	}
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("probe вернул ошибку: %+v", res)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("не текстовый результат: %T", res.Content[0])
	}
	var got probeResult
	if err := json.Unmarshal([]byte(text.Text), &got); err != nil {
		t.Fatalf("%v: %s", err, text.Text)
	}

	t.Logf("из песочницы: %+v", got)

	if got.Home != "hidden" {
		t.Errorf("/home виден из песочницы: %+v", got)
	}
	if got.SSH != "denied" {
		t.Errorf("~/.ssh читается: %+v", got)
	}
	if got.Net != "blocked" {
		t.Errorf("сеть доступна: %+v", got)
	}
	if got.Write != "denied" {
		t.Errorf("запись в домашний каталог прошла: %+v", got)
	}
	if got.ProcRoot != "denied" {
		t.Errorf("/proc/1/root читается: %+v", got)
	}
	if got.Canary != "clean" {
		t.Errorf("переменная вне whitelist утекла: %+v", got)
	}

	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "mcprig-pwned")); err == nil {
		t.Fatal("файл-маркер появился на хосте: песочница не держит")
	}
	if !strings.HasPrefix(os.Getenv("PATH"), "/") {
		t.Fatalf("PATH окружения теста сломан: %q", os.Getenv("PATH"))
	}
}
