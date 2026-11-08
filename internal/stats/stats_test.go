package stats

import (
	"context"
	"errors"
	"testing"
	"time"
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
	oc := find(s.Counters, "opencode", "echo")
	if oc == nil || oc.Calls != 3 || oc.Failures != 1 || oc.TotalMs != 40 {
		t.Fatalf("opencode/echo = %+v", oc)
	}
	if cc := find(s.Counters, "claude-code", "fetch"); cc == nil || cc.Calls != 1 {
		t.Fatalf("claude-code/fetch = %+v", cc)
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
	c := find(r.Snapshot().Counters, "a", "t")
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

func find(cs []Counter, client, tool string) *Counter {
	for i := range cs {
		if cs[i].Client == client && cs[i].Tool == tool {
			return &cs[i]
		}
	}
	return nil
}
