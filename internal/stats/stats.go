// Package stats collects per-client call counters for the panel.
//
// A "client" is whoever asked: a harness in stdio mode ("opencode",
// "claude-code", …) or a caller of the HTTP endpoint (key id plus the
// User-Agent product token). Counters live in memory; the panel reads them
// and the ring of recent calls for the tail of the dashboard.
//
// Токены не учитываются: у MCP-ответа нет стандартного поля usage, а формы
// _meta у серверов разные, поэтому счётчик намеренно без токенов.
package stats

import (
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// MaxLatencySamples bounds the per-(client, tool) latency reservoir used for
// percentiles. A call beyond the bound overwrites a slot round-robin, so the
// percentiles describe recent traffic rather than all of it.
const MaxLatencySamples = 256

// MaxLabelBytes — потолок метки клиента и имени инструмента: и то и другое
// приходит снаружи (заголовок User-Agent, clientInfo из initialize, имя
// инструмента в invoke_command), поэтому длина не доверяется.
const MaxLabelBytes = 64

// MaxCounters — жёсткий потолок числа счётчиков. Пара (клиент, инструмент)
// создаётся по запросу, и без потолка память растёт от каждого нового
// User-Agent. Лишние пары падают в один счётчик OtherLabel.
const MaxCounters = 4096

// MaxMessageBytes — потолок текста ошибки в кольце вызовов. Текст приходит от
// апстрима и может нести тело запроса (до 4 МиБ), а кольцо — 512 записей:
// без потолка один набор занимал бы гигабайты.
const MaxMessageBytes = 512

// cleanMessage обрезает текст ошибки до MaxMessageBytes по границе руны и
// помечает обрезку многоточием.
func cleanMessage(s string) string {
	if len(s) <= MaxMessageBytes {
		return s
	}
	cut := MaxMessageBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// OtherLabel — метка счётчика, вбирающего пары сверх MaxCounters.
const OtherLabel = "(прочее)"

// CleanLabel обрезает внешнюю метку до MaxLabelBytes, выбрасывает управляющие
// символы и чинит разрезанную на границе руну. Тот же вид получает подпись
// ключа в API: счётчик и /api/sets показывают одно и то же.
func CleanLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > MaxLabelBytes {
		cut := MaxLabelBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}

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

// Counter is the aggregate for one (client, server, tool) triple.
type Counter struct {
	Client   string `json:"client"`
	Server   string `json:"server"`
	Tool     string `json:"tool"`
	Calls    int64  `json:"calls"`
	Failures int64  `json:"failures"`
	TotalMs  int64  `json:"total_ms"`
	// Перцентили заполняются при сборке Snapshot: в Record это стоило бы
	// сортировки резерва на каждый вызов.
	P50Ms int64 `json:"p50ms"`
	P95Ms int64 `json:"p95ms"`

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
	schema   *Schema
	// hist, when set, mirrors every call to the on-disk history.
	hist *History
	// client, when set, overrides the caller's own label: a stdio process has
	// exactly one client and names it after the transport.
	client string
}

// counterKey включает сервер: два сервера с одинаковым именем инструмента —
// две разные строки дашборда.
type counterKey struct{ server, client, tool string }

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

// SetClientLabel fixes the client name for a process that has exactly one
// client: in stdio the transport is the boundary, so the history line says
// "stdio" rather than whatever the harness calls itself.
func (r *Recorder) SetClientLabel(label string) {
	r.mu.Lock()
	r.client = label
	r.mu.Unlock()
}

// Record accounts one finished invocation. Метки приходят от клиента, поэтому
// сначала чистятся, а пары сверх потолка складываются в OtherLabel.
func (r *Recorder) Record(client, server, tool string, d time.Duration, err error) {
	client = CleanLabel(client)
	tool = CleanLabel(tool)
	if client == "" {
		client = "unknown"
	}
	ms := d.Milliseconds()
	c := Call{Time: time.Now(), Client: client, Set: r.set, Server: server, Tool: tool, Millis: ms}
	if err != nil {
		c.Failed = true
		c.Message = cleanMessage(err.Error())
	}

	r.mu.Lock()
	if r.client != "" {
		client = r.client
		c.Client = client
	}

	k := counterKey{server: server, client: client, tool: tool}
	ctr := r.counters[k]
	if ctr == nil {
		// Последний слот зарезервирован под OtherLabel, поэтому реальных пар
		// не больше MaxCounters-1, а всего счётчиков — не больше MaxCounters.
		if len(r.counters) >= MaxCounters-1 {
			k = counterKey{server: OtherLabel, client: OtherLabel, tool: OtherLabel}
			ctr = r.counters[k]
		}
		if ctr == nil {
			ctr = &Counter{Client: k.client, Server: k.server, Tool: k.tool, latencies: make([]int64, 0, 16)}
			r.counters[k] = ctr
		}
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
	hist := r.hist
	r.mu.Unlock()

	// Канал истории неблокирующий: строка теряется только при переполнении.
	if hist != nil {
		hist.Record(client, server, tool, ms, err != nil)
	}
}

// SetHistory mirrors every recorded call to the on-disk history.
func (r *Recorder) SetHistory(h *History) {
	r.mu.Lock()
	r.hist = h
	r.mu.Unlock()
}

// Snapshot is the panel view: counters with percentiles, newest tail first.
type Snapshot struct {
	Set      string    `json:"set,omitempty"`
	Counters []Counter `json:"counters"`
	Tail     []Call    `json:"tail"`
	// Schema — вес list_commands набора, если прокси уже снял его с кэша.
	Schema *Schema `json:"schema,omitempty"`
	// Stats — состояние истории на диске: сколько строк потерял полный канал.
	Stats *HistoryStats `json:"stats,omitempty"`
}

// HistoryStats is the disk-history health the panel shows as stats.dropped.
type HistoryStats struct {
	Dropped int64 `json:"dropped"`
}

// Schema is what list_commands costs the caller's context: the JSON of the
// whole-set answer in bytes, and the same in tokens at four bytes per token.
type Schema struct {
	Bytes  int64 `json:"schemaBytes"`
	Tokens int64 `json:"schemaTokens"`
	Tools  int   `json:"tools"`
}

// TokensOf estimates the context cost of an answer at four bytes per token,
// rounded up: the panel's rough yardstick, not a tokenizer.
func TokensOf(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

// SetSchema replaces the list_commands weight the panel shows for this set.
// The proxy pushes it whenever a tool cache changes, so /stats stays a plain
// snapshot and no extra downstream call is made.
func (r *Recorder) SetSchema(s Schema) {
	r.mu.Lock()
	r.schema = &s
	r.mu.Unlock()
}

// Snapshot copies the state. Counters are sorted by client, server, then tool,
// and carry their percentiles.
func (r *Recorder) Snapshot() Snapshot { return r.snapshot(false) }

// AnonymousLabel заменяет метку клиента в редакции для открытого набора.
const AnonymousLabel = "(аноним)"

// SnapshotRedacted — снимок для анонимного читателя открытого набора: тексты
// ошибок апстрима и метки клиентов не выходят наружу, остальное (счётчики,
// латентности, хвост) остаётся.
func (r *Recorder) SnapshotRedacted() Snapshot { return r.snapshot(true) }

// snapshot собирает снимок сразу в нужной редакции: второй снимок и второй
// проход по полной копии не нужны, а на открытом наборе /stats зовут анонимно.
func (r *Recorder) snapshot(redacted bool) Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := Snapshot{Set: r.set, Counters: make([]Counter, 0, len(r.counters)), Schema: r.schema}
	if r.hist != nil {
		out.Stats = &HistoryStats{Dropped: r.hist.Dropped()}
	}
	for _, c := range r.counters {
		cp := *c
		cp.latencies = append([]int64(nil), c.latencies...)
		cp.P50Ms = cp.P50()
		cp.P95Ms = cp.P95()
		if redacted {
			cp.Client = AnonymousLabel
		}
		out.Counters = append(out.Counters, cp)
	}
	sortCounters(out.Counters)

	out.Tail = make([]Call, 0, len(r.tail))
	for i := 0; i < len(r.tail); i++ {
		idx := (r.tailNext - 1 - i + len(r.tail)) % len(r.tail)
		c := r.tail[idx]
		if redacted {
			c.Client = AnonymousLabel
			c.Message = ""
		}
		out.Tail = append(out.Tail, c)
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
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	i := int(q * float64(len(sorted)-1))
	return sorted[i]
}

func sortCounters(cs []Counter) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Client != cs[j].Client {
			return cs[i].Client < cs[j].Client
		}
		if cs[i].Server != cs[j].Server {
			return cs[i].Server < cs[j].Server
		}
		return cs[i].Tool < cs[j].Tool
	})
}
