package stats

// Разрез истории по периодам: сегодня, 7 суток, 30 суток, архив.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Периоды, которые принимает панель.
const (
	PeriodToday   = "today"
	Period7d      = "7d"
	Period30d     = "30d"
	PeriodArchive = "archive"
)

// Query is one slice of the history: period, series step and filters.
type Query struct {
	Period string
	// Bucket — шаг графика. hour|day; где данные грубее, шаг задаёт источник.
	Bucket string
	Server string
	Tool   string
	Client string
}

// Totals is the invariant part of every answer.
type Totals struct {
	Calls    int64 `json:"calls"`
	Failures int64 `json:"failures"`
	P50Ms    int64 `json:"p50ms"`
	P95Ms    int64 `json:"p95ms"`
}

// Point is one point of the series.
type Point struct {
	T        time.Time `json:"t"`
	Calls    int64     `json:"calls"`
	Failures int64     `json:"failures"`
	P50Ms    int64     `json:"p50ms"`
	P95Ms    int64     `json:"p95ms"`
}

// Row is one line of the tools, servers or clients breakdown.
type Row struct {
	Client   string `json:"client,omitempty"`
	Server   string `json:"server,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Calls    int64  `json:"calls"`
	Failures int64  `json:"failures"`
	P50Ms    int64  `json:"p50ms"`
	P95Ms    int64  `json:"p95ms"`
}

// Result is the panel answer for one period.
type Result struct {
	Period string    `json:"period"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	// Approximate: процентили посчитаны по гистограмме (30d и архив), а не по
	// точным значениям сырого журнала.
	Approximate bool     `json:"approximate"`
	Totals      Totals   `json:"totals"`
	Series      []Point  `json:"series"`
	Tools       []Row    `json:"tools"`
	Servers     []Row    `json:"servers"`
	Clients     []Row    `json:"clients"`
	Unreachable []string `json:"unreachable,omitempty"`
}

// step is the series granularity.
type step int

const (
	stepHour step = iota
	stepDay
	stepMonth
)

func (s step) truncate(t time.Time) time.Time {
	switch s {
	case stepHour:
		return t.Truncate(time.Hour)
	case stepMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	default:
		return startOfDay(t)
	}
}

// acc accumulates one group of calls, exactly and as a histogram at once: the
// exact list is unused when the answer is approximate.
type acc struct {
	calls    int64
	failures int64
	exact    []int64
	hist     Hist
}

func (a *acc) addCall(ms int64, failed bool) {
	a.calls++
	if failed {
		a.failures++
	}
	a.exact = append(a.exact, ms)
	a.hist[histIndex(ms)]++
}

func (a *acc) addTriple(t Triple) {
	a.calls += t.Calls
	a.failures += t.Failures
	for i, n := range t.Hist {
		a.hist[i] += n
	}
}

func (a *acc) totals(approx bool) Totals {
	p50, p95 := a.percentiles(approx)
	return Totals{Calls: a.calls, Failures: a.failures, P50Ms: p50, P95Ms: p95}
}

// percentiles: exact from the raw values when the period is ≤ 7d, otherwise
// the middle of the histogram bucket.
func (a *acc) percentiles(approx bool) (int64, int64) {
	if !approx {
		return quantile(a.exact, 0.50), quantile(a.exact, 0.95)
	}
	return histQuantile(a.hist, 0.50), histQuantile(a.hist, 0.95)
}

// histQuantile is the bucket where rank lands, reported by its middle.
func histQuantile(h Hist, q float64) int64 {
	var n int64
	for _, c := range h {
		n += c
	}
	if n == 0 {
		return 0
	}
	rank := int64(q * float64(n-1))
	var cum int64
	for i, c := range h {
		cum += c
		if rank < cum {
			return histMid(i)
		}
	}
	return histMid(len(h) - 1)
}

// bundle aggregates every group one answer needs in a single pass.
type bundle struct {
	step    step
	approx  bool
	filter  Query
	totals  acc
	tools   map[[2]string]*acc
	servers map[string]*acc
	clients map[string]*acc
	series  map[time.Time]*acc
}

func newBundle(st step, approx bool, filter Query) *bundle {
	return &bundle{
		step:    st,
		approx:  approx,
		filter:  filter,
		tools:   map[[2]string]*acc{},
		servers: map[string]*acc{},
		clients: map[string]*acc{},
		series:  map[time.Time]*acc{},
	}
}

func (b *bundle) matches(client, server, tool string) bool {
	return (b.filter.Client == "" || b.filter.Client == client) &&
		(b.filter.Server == "" || b.filter.Server == server) &&
		(b.filter.Tool == "" || b.filter.Tool == tool)
}

func (b *bundle) addRaw(l rawLine) {
	if !b.matches(l.Client, l.Server, l.Tool) {
		return
	}
	for _, a := range b.groups(l.Time, l.Client, l.Server, l.Tool) {
		a.addCall(l.MS, l.Failed)
	}
}

func (b *bundle) addTriple(t Triple, when time.Time) {
	if !b.matches(t.Client, t.Server, t.Tool) {
		return
	}
	for _, a := range b.groups(when, t.Client, t.Server, t.Tool) {
		a.addTriple(t)
	}
}

func (b *bundle) groups(when time.Time, client, server, tool string) []*acc {
	return []*acc{
		&b.totals,
		bucketOf(&b.tools, [2]string{server, tool}),
		bucketOf(&b.servers, server),
		bucketOf(&b.clients, client),
		bucketOf(&b.series, b.step.truncate(when)),
	}
}

func bucketOf[K comparable](m *map[K]*acc, k K) *acc {
	a := (*m)[k]
	if a == nil {
		a = &acc{}
		(*m)[k] = a
	}
	return a
}

func (b *bundle) result(period string, from, to time.Time) Result {
	res := Result{
		Period:      period,
		From:        from,
		To:          to,
		Approximate: b.approx,
		Totals:      b.totals.totals(b.approx),
		Series:      make([]Point, 0, len(b.series)),
		Tools:       make([]Row, 0, len(b.tools)),
		Servers:     make([]Row, 0, len(b.servers)),
		Clients:     make([]Row, 0, len(b.clients)),
	}
	for t, a := range b.series {
		tot := a.totals(b.approx)
		res.Series = append(res.Series, Point{T: t, Calls: tot.Calls, Failures: tot.Failures, P50Ms: tot.P50Ms, P95Ms: tot.P95Ms})
	}
	sort.Slice(res.Series, func(i, j int) bool { return res.Series[i].T.Before(res.Series[j].T) })

	for k, a := range b.tools {
		res.Tools = append(res.Tools, rowOf(Row{Server: k[0], Tool: k[1]}, a, b.approx))
	}
	sort.Slice(res.Tools, func(i, j int) bool {
		if res.Tools[i].Server != res.Tools[j].Server {
			return res.Tools[i].Server < res.Tools[j].Server
		}
		return res.Tools[i].Tool < res.Tools[j].Tool
	})
	for k, a := range b.servers {
		res.Servers = append(res.Servers, rowOf(Row{Server: k}, a, b.approx))
	}
	sort.Slice(res.Servers, func(i, j int) bool { return res.Servers[i].Server < res.Servers[j].Server })
	for k, a := range b.clients {
		res.Clients = append(res.Clients, rowOf(Row{Client: k}, a, b.approx))
	}
	sort.Slice(res.Clients, func(i, j int) bool { return res.Clients[i].Client < res.Clients[j].Client })
	return res
}

func rowOf(r Row, a *acc, approx bool) Row {
	t := a.totals(approx)
	r.Calls, r.Failures, r.P50Ms, r.P95Ms = t.Calls, t.Failures, t.P50Ms, t.P95Ms
	return r
}

// QueryHistory answers one period from the given stats directories — one per
// set, so set=all is just every directory. A missing directory contributes
// nothing, a broken journal line is skipped.
func QueryHistory(dirs []string, q Query, now time.Time) Result {
	today := startOfDay(now)
	switch q.Period {
	case PeriodToday:
		b := newBundle(q.stepFor(stepHour), false, q)
		for _, dir := range dirs {
			readRawDay(dir, today, b)
		}
		return b.result(q.Period, today, now)

	case Period7d:
		b := newBundle(q.stepFor(stepHour), false, q)
		for _, dir := range dirs {
			for i := 0; i < 7; i++ {
				readRawDay(dir, today.AddDate(0, 0, -i), b)
			}
		}
		return b.result(q.Period, today.AddDate(0, 0, -6), now)

	case Period30d:
		b := newBundle(q.stepFor(stepDay), true, q)
		for _, dir := range dirs {
			readLast30(dir, today, b)
		}
		return b.result(q.Period, today.AddDate(0, 0, -(DayDays-1)), now)

	case PeriodArchive:
		b := newBundle(q.stepFor(stepMonth), true, q)
		archiveStart := today.AddDate(0, 0, -(DayDays - 1))
		from := time.Time{}
		for _, dir := range dirs {
			if d := readMonths(dir, b); !d.IsZero() && (from.IsZero() || d.Before(from)) {
				from = d
			}
		}
		if from.IsZero() {
			from = archiveStart
		}
		return b.result(q.Period, from, archiveStart)
	}
	return Result{Period: q.Period, From: today, To: today}
}

// stepFor: bucket=day|hour asks for that step; anything finer than the source
// (hour on day files) keeps the source step.
func (q Query) stepFor(dflt step) step {
	switch q.Bucket {
	case "day":
		return stepDay
	case "month":
		return stepMonth
	}
	return dflt
}

func readRawDay(dir string, day time.Time, b *bundle) {
	_ = scanRaw(filepath.Join(dir, "raw-"+day.Format(dayLayout)+".jsonl"), b.addRaw)
}

// readLast30 prefers the day file and falls back to the raw journal of days
// the rotation has not folded yet (today and everything since it last ran).
func readLast30(dir string, today time.Time, b *bundle) {
	for i := 0; i < DayDays; i++ {
		day := today.AddDate(0, 0, -i)
		if df, err := readDayFile(filepath.Join(dir, "day-"+day.Format(dayLayout)+".json")); err == nil {
			for _, t := range df.Triples {
				b.addTriple(t, day)
			}
			continue
		}
		readRawDay(dir, day, b)
	}
}

// readMonths feeds month files and reports the earliest archived day.
func readMonths(dir string, b *bundle) time.Time {
	paths, _ := filepath.Glob(filepath.Join(dir, "month-*.json"))
	from := time.Time{}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var mf monthFile
		if json.Unmarshal(raw, &mf) != nil {
			continue
		}
		for day, triples := range mf.Days {
			d, err := time.ParseInLocation(dayLayout, day, time.Local)
			if err != nil {
				continue
			}
			if from.IsZero() || d.Before(from) {
				from = d
			}
			for _, t := range triples {
				b.addTriple(t, d)
			}
		}
	}
	return from
}
