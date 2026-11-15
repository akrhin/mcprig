package mcpserver

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/stats"
)

func rateManager(t *testing.T, rl *config.RateLimit, names ...string) (*Manager, *mcp.ClientSession) {
	t.Helper()
	m := fanManager(t, newFanTracker(), &config.Config{RateLimit: rl}, names...)
	m.Stats = stats.NewRecorder("test", 0)
	return m, clientFor(t, m.Server())
}

var retryMS = regexp.MustCompile(`повторите через (\d+) мс`)

// retryOf returns the delay in the refusal text; it fails when the text is not
// the rate limit refusal or the delay is zero.
func retryOf(t *testing.T, res *mcp.CallToolResult) int64 {
	t.Helper()
	text := resultText(t, res)
	found := retryMS.FindStringSubmatch(text)
	if found == nil {
		t.Fatalf("не отказ лимита: %q", text)
	}
	ms, err := strconv.ParseInt(found[1], 10, 64)
	if err != nil || ms <= 0 {
		t.Fatalf("задержка из текста %q: %v", text, err)
	}
	return ms
}

// A set without rateLimit behaves as before: no limit at all.
func TestRateLimitAbsentIsNoLimit(t *testing.T) {
	m, cs := rateManager(t, nil, "a")
	for i := 0; i < 1000; i++ {
		if res := call(t, cs, "invoke_command", echoCall("a", "x", 0)); res.IsError {
			t.Fatalf("вызов %d отвергнут: %s", i, resultText(t, res))
		}
	}
	if got := m.Stats.Snapshot().Throttled; got != 0 {
		t.Fatalf("throttled = %d, want 0", got)
	}
}

// The second call over a burst of one is refused with the retry delay, and the
// refusal is counted.
func TestRateLimitRefusesOverLimit(t *testing.T) {
	m, cs := rateManager(t, &config.RateLimit{PerMinute: 60, Burst: 1}, "a")
	if res := call(t, cs, "invoke_command", echoCall("a", "first", 0)); res.IsError {
		t.Fatalf("первый вызов: %s", resultText(t, res))
	}
	res := call(t, cs, "invoke_command", echoCall("a", "second", 0))
	if !res.IsError {
		t.Fatal("второй вызов прошёл мимо лимита")
	}
	text := resultText(t, res)
	if !strings.Contains(text, "лимит 60 вызовов в минуту для a/echo") {
		t.Fatalf("текст отказа: %q", text)
	}
	if ms := retryOf(t, res); ms < 900 {
		t.Fatalf("задержка %d мс: при 60/мин ожидались ~1000", ms)
	}
	if got := m.Stats.Snapshot().Throttled; got != 1 {
		t.Fatalf("throttled = %d, want 1", got)
	}
	if got := m.Stats.Snapshot().ThrottledTools["a/echo"]; got != 1 {
		t.Fatalf("throttledTools[a/echo] = %d, want 1", got)
	}
}

// perTool overrides the set-wide limit, and the general limit still applies to
// the tools it does not name.
func TestRateLimitPerToolOverridesGeneral(t *testing.T) {
	rl := &config.RateLimit{
		PerMinute: 60, Burst: 1,
		PerTool: map[string]config.RateLimit{"a/echo": {PerMinute: 10, Burst: 3}},
	}
	_, cs := rateManager(t, rl, "a")
	for i := 0; i < 3; i++ {
		if res := call(t, cs, "invoke_command", echoCall("a", "x", 0)); res.IsError {
			t.Fatalf("echo %d: %s", i, resultText(t, res))
		}
	}
	if res := call(t, cs, "invoke_command", echoCall("a", "x", 0)); !res.IsError {
		t.Fatal("четвёртый echo прошёл мимо perTool")
	} else if text := resultText(t, res); !strings.Contains(text, "лимит 10 вызовов в минуту для a/echo") {
		t.Fatalf("текст отказа: %q", text)
	}
	if res := call(t, cs, "invoke_command", map[string]any{"server": "a", "command_name": "big", "parameters": map[string]any{"n": 1}}); res.IsError {
		t.Fatalf("big: %s", resultText(t, res))
	}
	// another tool of the same server has its own bucket with the general limit
	if res := call(t, cs, "invoke_command", map[string]any{"server": "a", "command_name": "big", "parameters": map[string]any{"n": 1}}); !res.IsError {
		t.Fatal("общий лимит не применился к a/big")
	} else if text := resultText(t, res); !strings.Contains(text, "лимит 60 вызовов в минуту для a/big") {
		t.Fatalf("текст отказа: %q", text)
	}
}

// invoke_many spends one token per inner call, from the same bucket as a single
// invoke_command.
func TestRateLimitInvokeManySpendsEachCall(t *testing.T) {
	m, cs := rateManager(t, &config.RateLimit{PerMinute: 60, Burst: 2}, "a")
	res := call(t, cs, "invoke_many", map[string]any{"calls": []any{
		echoCall("a", "one", 0),
		echoCall("a", "two", 0),
		echoCall("a", "three", 0),
	}})
	if res.IsError {
		t.Fatalf("invoke_many: %s", resultText(t, res))
	}
	ok, refused := 0, 0
	for _, ans := range manyAnswers(t, res) {
		switch {
		case ans.OK:
			ok++
		case strings.Contains(ans.Error, "лимит 60 вызовов в минуту для a/echo"):
			refused++
		default:
			t.Fatalf("неожиданная ошибка: %+v", ans)
		}
	}
	if ok != 2 || refused != 1 {
		t.Fatalf("прошло %d, отвергнуто %d; want 2 и 1", ok, refused)
	}
	if got := m.Stats.Snapshot().Throttled; got != 1 {
		t.Fatalf("throttled = %d, want 1", got)
	}
	// The fan-out drained the same bucket: a single call now meets the limit too.
	if res := call(t, cs, "invoke_command", echoCall("a", "after", 0)); !res.IsError {
		t.Fatal("invoke_command не увидел корзину invoke_many")
	}
}

// Two key fingerprints never share a bucket; the same key has one.
func TestRateLimitDoesNotLeakBetweenKeys(t *testing.T) {
	rl := &config.RateLimit{PerMinute: 60, Burst: 1}
	m := fanManager(t, newFanTracker(), &config.Config{RateLimit: rl}, "a")
	ctxA := stats.WithClientKey(context.Background(), "aaaaaa")
	ctxB := stats.WithClientKey(context.Background(), "bbbbbb")
	if got := identityOf(ctxA, nil); got != "key:aaaaaa" {
		t.Fatalf("identityOf = %q, want key:aaaaaa", got)
	}
	if err := m.throttle(ctxA, nil, "a", "echo"); err != nil {
		t.Fatalf("первый вызов ключа A: %v", err)
	}
	if err := m.throttle(ctxA, nil, "a", "echo"); err == nil {
		t.Fatal("второй вызов ключа A прошёл мимо лимита")
	}
	if err := m.throttle(ctxB, nil, "a", "echo"); err != nil {
		t.Fatalf("ключ B делит корзину с A: %v", err)
	}
}

// clientReq builds the request a client would send with its own clientInfo.
func clientReq(name string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "invoke_command",
		Meta: mcp.Meta{mcp.MetaKeyClientInfo: &mcp.Implementation{Name: name, Version: "1"}},
	}}
}

// A named key keeps one bucket no matter what _meta clientInfo the caller
// sends: clientInfo is self-declared, and changing it must not mint a quota.
func TestRateLimitNamedKeyIgnoresClientInfo(t *testing.T) {
	rl := &config.RateLimit{PerMinute: 60, Burst: 1}
	m := fanManager(t, newFanTracker(), &config.Config{RateLimit: rl}, "a")
	// The key is named, so the display label carries the note, not the
	// fingerprint; only the transport's key value identifies the caller.
	ctx := stats.WithClient(context.Background(), "named-caller")
	ctx = stats.WithClientKey(ctx, "aaaaaa")
	one, two := clientReq("one"), clientReq("two")
	if labelOf(ctx, one) == labelOf(ctx, two) {
		t.Fatal("clientInfo не различает метки — тест не про то")
	}
	if err := m.throttle(ctx, one, "a", "echo"); err != nil {
		t.Fatalf("первый вызов: %v", err)
	}
	if err := m.throttle(ctx, two, "a", "echo"); err == nil {
		t.Fatal("смена _meta clientInfo дала новую квоту")
	}
}

// Two keys with the same note are still two callers: the note is display only.
func TestRateLimitSameSignatureDifferentKeys(t *testing.T) {
	rl := &config.RateLimit{PerMinute: 60, Burst: 1}
	m := fanManager(t, newFanTracker(), &config.Config{RateLimit: rl}, "a")
	named := func(fp string) context.Context {
		ctx := stats.WithClient(context.Background(), "same-note")
		return stats.WithClientKey(ctx, fp)
	}
	first, second := named("aaaaaa"), named("bbbbbb")
	if err := m.throttle(first, nil, "a", "echo"); err != nil {
		t.Fatalf("ключ 1, первый вызов: %v", err)
	}
	if err := m.throttle(first, nil, "a", "echo"); err == nil {
		t.Fatal("второй вызов ключа 1 прошёл мимо лимита")
	}
	if err := m.throttle(second, nil, "a", "echo"); err != nil {
		t.Fatalf("ключ 2 с той же подписью делит корзину с ключом 1: %v", err)
	}
}

// burst: 0 means the whole perMinute quota passes at once.
func TestRateLimitBurstDefaultsToPerMinute(t *testing.T) {
	_, cs := rateManager(t, &config.RateLimit{PerMinute: 60}, "a")
	for i := 0; i < 60; i++ {
		if res := call(t, cs, "invoke_command", echoCall("a", "x", 0)); res.IsError {
			t.Fatalf("вызов %d из 60: %s", i, resultText(t, res))
		}
	}
	if res := call(t, cs, "invoke_command", echoCall("a", "x", 0)); !res.IsError {
		t.Fatal("61-й вызов прошёл: burst не взял perMinute")
	}
}

// perTool с perMinute: 0 снимает общий лимит с этого инструмента.
func TestRateLimitPerToolZeroLifts(t *testing.T) {
	rl := &config.RateLimit{
		PerMinute: 60, Burst: 1,
		PerTool: map[string]config.RateLimit{"a/echo": {}},
	}
	_, cs := rateManager(t, rl, "a")
	for i := 0; i < 50; i++ {
		if res := call(t, cs, "invoke_command", echoCall("a", "x", 0)); res.IsError {
			t.Fatalf("echo %d: общий лимит не снят для a/echo: %s", i, resultText(t, res))
		}
	}
	// Другой инструмент того же сервера живёт под общим лимитом.
	if res := call(t, cs, "invoke_command", map[string]any{"server": "a", "command_name": "big", "parameters": map[string]any{"n": 1}}); res.IsError {
		t.Fatalf("big: %s", resultText(t, res))
	}
	if res := call(t, cs, "invoke_command", map[string]any{"server": "a", "command_name": "big", "parameters": map[string]any{"n": 1}}); !res.IsError {
		t.Fatal("общий лимит не применился к a/big")
	}
}

// Поток уникальных пар не растит карту сверх потолка.
func TestRateLimitBucketCap(t *testing.T) {
	l := newRateLimits(&config.RateLimit{PerMinute: 60, Burst: 1})
	for i := 0; i < maxBuckets*2; i++ {
		l.allow("key:x", "s", "tool"+strconv.Itoa(i))
	}
	if got := len(l.buckets); got > maxBuckets {
		t.Fatalf("корзин %d, потолок %d", got, maxBuckets)
	}
}
