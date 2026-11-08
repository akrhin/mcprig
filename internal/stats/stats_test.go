package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRecordAggregatesPerClientAndTool(t *testing.T) {
	r := NewRecorder("demo", 4)
	r.Record("opencode", "alpha", "echo", 10*time.Millisecond, nil)
	r.Record("opencode", "alpha", "echo", 30*time.Millisecond, nil)
	r.Record("opencode", "alpha", "echo", 0, errors.New("boom"))
	r.Record("claude-code", "beta", "fetch", 500*time.Millisecond, nil)

	s := r.Snapshot()
	if len(s.Counters) != 2 {
		t.Fatalf("counters = %d, want 2", len(s.Counters))
	}
	// sorted by client then tool: claude-code/fetch, opencode/echo.
	oc := find(s.Counters, "alpha", "opencode", "echo")
	if oc == nil || oc.Calls != 3 || oc.Failures != 1 || oc.TotalMs != 40 {
		t.Fatalf("opencode/echo = %+v", oc)
	}
	if cc := find(s.Counters, "beta", "claude-code", "fetch"); cc == nil || cc.Calls != 1 {
		t.Fatalf("claude-code/fetch = %+v", cc)
	}
}

// Один и тот же инструмент на двух серверах — две строки: сервер входит в ключ.
func TestCountersSplitByServer(t *testing.T) {
	r := NewRecorder("demo", 4)
	r.Record("opencode", "alpha", "search", 10*time.Millisecond, nil)
	r.Record("opencode", "beta", "search", 20*time.Millisecond, nil)
	r.Record("opencode", "alpha", "search", 30*time.Millisecond, nil)

	s := r.Snapshot()
	if len(s.Counters) != 2 {
		t.Fatalf("counters = %+v", s.Counters)
	}
	a := find(s.Counters, "alpha", "opencode", "search")
	b := find(s.Counters, "beta", "opencode", "search")
	if a == nil || a.Calls != 2 || b == nil || b.Calls != 1 {
		t.Fatalf("alpha = %+v, beta = %+v", a, b)
	}
	if a.Server != "alpha" || b.Server != "beta" {
		t.Fatalf("поле server не заполнено: %+v, %+v", a, b)
	}
}

// Перцентили попадают в снимок в миллисекундах.
func TestSnapshotPercentiles(t *testing.T) {
	r := NewRecorder("demo", 4)
	for i := 1; i <= 100; i++ {
		r.Record("opencode", "alpha", "echo", time.Duration(i)*time.Millisecond, nil)
	}
	c := find(r.Snapshot().Counters, "alpha", "opencode", "echo")
	if c == nil || c.P50Ms != 50 || c.P95Ms != 95 {
		t.Fatalf("перцентили = %+v", c)
	}
}

func TestTailIsNewestFirstAndBounded(t *testing.T) {
	r := NewRecorder("demo", 2)
	r.Record("a", "s", "t1", 0, nil)
	r.Record("a", "s", "t2", 0, nil)
	r.Record("a", "s", "t3", 0, errors.New("nope"))

	tail := r.Snapshot().Tail
	if len(tail) != 2 {
		t.Fatalf("tail len = %d, want 2", len(tail))
	}
	if tail[0].Tool != "t3" || tail[1].Tool != "t2" {
		t.Fatalf("tail order = %q, %q", tail[0].Tool, tail[1].Tool)
	}
	if !tail[0].Failed || tail[0].Message != "nope" {
		t.Fatalf("failure not recorded: %+v", tail[0])
	}
}

func TestQuantilesAndOverflow(t *testing.T) {
	r := NewRecorder("demo", 0)
	for i := 1; i <= MaxLatencySamples+10; i++ {
		r.Record("a", "s", "t", time.Duration(i)*time.Millisecond, nil)
	}
	c := find(r.Snapshot().Counters, "s", "a", "t")
	if c == nil || c.Calls != int64(MaxLatencySamples+10) {
		t.Fatalf("counter = %+v", c)
	}
	if len(c.latencies) != MaxLatencySamples {
		t.Fatalf("reservoir = %d, want %d", len(c.latencies), MaxLatencySamples)
	}
	if p50, p95 := c.P50(), c.P95(); p50 <= 0 || p95 < p50 {
		t.Fatalf("p50=%d p95=%d", p50, p95)
	}
}

func TestClientLabelFromContext(t *testing.T) {
	if got := ClientFrom(context.Background(), "stdio"); got != "stdio" {
		t.Fatalf("fallback = %q", got)
	}
	ctx := WithClient(context.Background(), "opencode")
	if got := ClientFrom(ctx, "stdio"); got != "opencode" {
		t.Fatalf("ctx = %q", got)
	}
	if got := Label("abc123", "opencode/2.2.6"); got != "key:abc123/opencode/2.2.6" {
		t.Fatalf("label = %q", got)
	}
	if got := Label("", "opencode/2.2.6"); got != "opencode/2.2.6" {
		t.Fatalf("label = %q", got)
	}
}

func find(cs []Counter, server, client, tool string) *Counter {
	for i := range cs {
		if cs[i].Server == server && cs[i].Client == client && cs[i].Tool == tool {
			return &cs[i]
		}
	}
	return nil
}

// Потолок: 5000 разных меток не растят карту дальше MaxCounters, лишние пары
// уходят в один счётчик (прочее), и ни один вызов не теряется.
func TestCounterCap(t *testing.T) {
	r := NewRecorder("demo", 0)
	const n = 5000
	for i := 0; i < n; i++ {
		r.Record(fmt.Sprintf("client-%d", i), "s", fmt.Sprintf("tool-%d", i), 0, nil)
	}
	s := r.Snapshot()
	if len(s.Counters) > MaxCounters {
		t.Fatalf("счётчиков %d, потолок %d", len(s.Counters), MaxCounters)
	}
	var calls int64
	var other *Counter
	for i := range s.Counters {
		calls += s.Counters[i].Calls
		if s.Counters[i].Client == OtherLabel && s.Counters[i].Tool == OtherLabel {
			other = &s.Counters[i]
		}
	}
	if calls != n {
		t.Fatalf("сумма вызовов %d, ожидалось %d", calls, n)
	}
	if other == nil || other.Calls != n-(MaxCounters-1) {
		t.Fatalf("счётчик (прочее) = %+v, ожидалось %d вызовов", other, n-(MaxCounters-1))
	}
	if cc := s.Counters[0]; cc.Client == "client-0" && cap(cc.latencies) > 64 {
		t.Fatalf("латентностный резерв под счётчик = %d, ожидался маленький старт", cap(cc.latencies))
	}
}

// Память не растёт линейно: после насыщения потолка новая тысяча меток не
// добавляет ни счётчиков, ни заметной кучи.
func TestCounterCapBoundsMemory(t *testing.T) {
	r := NewRecorder("demo", 0)
	record := func(from, to int) {
		for i := from; i < to; i++ {
			r.Record(fmt.Sprintf("client-%d", i), "s", fmt.Sprintf("tool-%d", i), 0, nil)
		}
	}
	record(0, 5000)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	record(5000, 45000)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if got := len(r.Snapshot().Counters); got > MaxCounters {
		t.Fatalf("счётчиков %d, потолок %d", got, MaxCounters)
	}
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 4<<20 {
		t.Fatalf("куча выросла на %d байт на 40000 меток — потолок не работает", grown)
	}
}

// Внешняя метка не сохраняется целиком: 1 МиБ обрезается, управляющие
// символы вычищаются.
func TestLabelIsBoundedAndClean(t *testing.T) {
	r := NewRecorder("demo", 4)
	r.Record(strings.Repeat("A", 1<<20), "s", "tool\x00\x1b[31mred", 0, nil)

	s := r.Snapshot()
	if len(s.Counters) != 1 {
		t.Fatalf("счётчиков %d, ожидался 1", len(s.Counters))
	}
	c := s.Counters[0]
	if len(c.Client) > MaxLabelBytes || len(c.Client) != MaxLabelBytes {
		t.Fatalf("метка клиента длиной %d, ожидалось %d", len(c.Client), MaxLabelBytes)
	}
	if strings.ContainsAny(c.Tool, "\x00\x1b") {
		t.Fatalf("в имени инструмента остались управляющие символы: %q", c.Tool)
	}
	if len(s.Tail[0].Client) > MaxLabelBytes {
		t.Fatalf("метка в хвосте длиной %d", len(s.Tail[0].Client))
	}
	if got := cleanLabel("  "); got != "  " {
		t.Fatalf("cleanLabel не должен трогать короткие метки: %q", got)
	}
	if got := cleanLabel(strings.Repeat("ж", 100)); len(got) > MaxLabelBytes || !utf8.ValidString(got) {
		t.Fatalf("обрезка по байтам разрезала руну: %q (%d байт)", got, len(got))
	}
}

// Текст ошибки в кольце не тянет тело запроса: 10 МиБ обрезаются до
// MaxMessageBytes, а снимок из 512 таких вызовов остаётся меньше 1 МиБ.
func TestErrorMessageIsBounded(t *testing.T) {
	huge := errors.New(strings.Repeat("e", 10<<20))
	r := NewRecorder("demo", 512)
	for i := 0; i < 512; i++ {
		r.Record("opencode", "upstream", "echo", 0, huge)
	}

	s := r.Snapshot()
	if got := len(s.Tail); got != 512 {
		t.Fatalf("tail len = %d, ожидалось 512", got)
	}
	for _, c := range s.Tail {
		if len(c.Message) > MaxMessageBytes+len("…") {
			t.Fatalf("текст ошибки %d байт, потолок %d", len(c.Message), MaxMessageBytes)
		}
		if !c.Failed {
			t.Fatalf("потерян признак ошибки: %+v", c)
		}
	}

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(raw) >= 1<<20 {
		t.Fatalf("снимок %d байт, ожидалось < 1 МиБ", len(raw))
	}

	// Обрезка по границе руны: многоточие на конце, utf8 цел.
	r2 := NewRecorder("demo", 1)
	r2.Record("a", "s", "t", 0, errors.New(strings.Repeat("ж", 1000)))
	msg := r2.Snapshot().Tail[0].Message
	if len(msg) > MaxMessageBytes+len("…") || !utf8.ValidString(msg) || !strings.HasSuffix(msg, "…") {
		t.Fatalf("обрезка многоточием сломана: %d байт, utf8=%v", len(msg), utf8.ValidString(msg))
	}
}
