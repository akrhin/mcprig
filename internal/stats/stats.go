// Package stats collects per-client call counters for the panel.
//
// A "client" is whoever asked: a harness in stdio mode ("opencode",
// "claude-code", …) or a caller of the HTTP endpoint (key id plus the
// User-Agent product token). Counters live in memory; the panel reads them
// and the ring of recent calls for the tail of the dashboard.
package stats

import (
	"sync"
	"time"
)

// MaxLatencySamples bounds the per-(client, tool) latency reservoir used for
// percentiles. A call beyond the bound overwrites a slot round-robin, so the
// percentiles describe recent traffic rather than all of it.
const MaxLatencySamples = 256

// Call is one finished invocation, kept for the dashboard's tail.
type Call struct {
	Time    time.Time `json:"time"`
	Client  string    `json:"client"`
	Set     string    `json:"set,omitempty"`
	Server  string    `json:"server"`
	Tool    string    `json:"tool"`
	Millis  int64     `json:"ms"`
	Failed  bool      `json:"failed"`
	Message string    `json:"error,omitempty"`
}

// Counter is the aggregate for one (client, tool) pair.
type Counter struct {
	Client   string `json:"client"`
	Tool     string `json:"tool"`
	Calls    int64  `json:"calls"`
	Failures int64  `json:"failures"`
	TotalMs  int64  `json:"total_ms"`

	latencies []int64
	next      int
}

// Recorder is safe for concurrent use.
type Recorder struct {
	mu       sync.Mutex
	set      string
	counters map[counterKey]*Counter
	tail     []Call
	tailNext int
	tailCap  int
}

type counterKey struct{ client, tool string }

// NewRecorder makes a recorder for one set. tailCap bounds the recent-call
// ring (0 means 512).
func NewRecorder(set string, tailCap int) *Recorder {
	if tailCap <= 0 {
		tailCap = 512
	}
	return &Recorder{
		set:      set,
		counters: make(map[counterKey]*Counter),
		tail:     make([]Call, 0, tailCap),
		tailCap:  tailCap,
	}
}

// Record accounts one finished invocation.
func (r *Recorder) Record(client, server, tool string, d time.Duration, err error) {
	if client == "" {
		client = "unknown"
	}
	ms := d.Milliseconds()
	c := Call{Time: time.Now(), Client: client, Set: r.set, Server: server, Tool: tool, Millis: ms}
	if err != nil {
		c.Failed = true
		c.Message = err.Error()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	k := counterKey{client: client, tool: tool}
	ctr := r.counters[k]
	if ctr == nil {
		ctr = &Counter{Client: client, Tool: tool, latencies: make([]int64, 0, MaxLatencySamples)}
		r.counters[k] = ctr
	}
	ctr.Calls++
	ctr.TotalMs += ms
	if err != nil {
		ctr.Failures++
	}
	if len(ctr.latencies) < MaxLatencySamples {
		ctr.latencies = append(ctr.latencies, ms)
	} else {
		ctr.latencies[ctr.next] = ms
		ctr.next = (ctr.next + 1) % MaxLatencySamples
	}

	if len(r.tail) < r.tailCap {
		r.tail = append(r.tail, c)
	} else {
		r.tail[r.tailNext] = c
		r.tailNext = (r.tailNext + 1) % r.tailCap
	}
}

// Snapshot is the panel view: counters with percentiles, newest tail first.
type Snapshot struct {
	Set      string    `json:"set,omitempty"`
	Counters []Counter `json:"counters"`
	Tail     []Call    `json:"tail"`
}

// Snapshot copies the state. Counters are sorted by client, then tool.
func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := Snapshot{Set: r.set, Counters: make([]Counter, 0, len(r.counters))}
	for _, c := range r.counters {
		cp := *c
		cp.latencies = append([]int64(nil), c.latencies...)
		out.Counters = append(out.Counters, cp)
	}
	sortCounters(out.Counters)

	out.Tail = make([]Call, 0, len(r.tail))
	for i := 0; i < len(r.tail); i++ {
		idx := (r.tailNext - 1 - i + len(r.tail)) % len(r.tail)
		out.Tail = append(out.Tail, r.tail[idx])
	}
	return out
}

// P50 and P95 are computed from the latency reservoir; 0 when there is none.
func (c Counter) P50() int64 { return quantile(c.latencies, 0.50) }
func (c Counter) P95() int64 { return quantile(c.latencies, 0.95) }

func quantile(xs []int64, q float64) int64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]int64(nil), xs...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	i := int(q * float64(len(sorted)-1))
	return sorted[i]
}

func sortCounters(cs []Counter) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0; j-- {
			a, b := cs[j-1], cs[j]
			if a.Client < b.Client || (a.Client == b.Client && a.Tool <= b.Tool) {
				break
			}
			cs[j-1], cs[j] = b, a
		}
	}
}
