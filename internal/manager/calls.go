package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/akrhin/mcprig/internal/registry"
	"github.com/akrhin/mcprig/internal/stats"
)

const (
	callsDefaultLimit = 50
	callsMaxLimit     = 500
	callsTimeout      = 2 * time.Second
	callsConcurrency  = 4
)

// callRow — одна строка хвоста для панели. Error всегда в теле, даже пустой:
// так клиенту не надо отличать отсутствие поля от успеха.
type callRow struct {
	Time   time.Time `json:"time"`
	Set    string    `json:"set,omitempty"`
	Client string    `json:"client"`
	Server string    `json:"server"`
	Tool   string    `json:"tool"`
	MS     int64     `json:"ms"`
	Failed bool      `json:"failed"`
	Error  string    `json:"error"`
}

type counterRow struct {
	Client   string `json:"client"`
	Set      string `json:"set,omitempty"`
	Server   string `json:"server"`
	Tool     string `json:"tool"`
	Calls    int64  `json:"calls"`
	Failures int64  `json:"failures"`
	TotalMs  int64  `json:"totalMs"`
	P50Ms    int64  `json:"p50ms"`
	P95Ms    int64  `json:"p95ms"`
}

type callsResponse struct {
	Set         string        `json:"set,omitempty"`
	Calls       []callRow     `json:"calls"`
	Counters    []counterRow  `json:"counters"`
	Schema      *stats.Schema `json:"schema,omitempty"`
	Unreachable []string      `json:"unreachable,omitempty"`
}

// apiCalls собирает /stats запущенных наборов в один ответ панели.
// Остановленный набор — пустые массивы, недоступный — только в unreachable:
// текст ошибки чужого набора клиенту панели не уходит.
func (m *Manager) apiCalls(req *request) error {
	q := req.r.URL.Query()
	limit := callsLimit(q.Get("limit"))
	id := q.Get("set")
	out := callsResponse{Set: id, Calls: []callRow{}, Counters: []counterRow{}}

	type target struct {
		set  *registry.Set
		port int
	}
	targets := []target{}
	if id != "" {
		set := m.store.Set(id)
		if set == nil {
			return &httpError{http.StatusNotFound, "набор не найден"}
		}
		if port, running := m.runningPort(set); running {
			targets = append(targets, target{set, port})
		}
	} else {
		for _, set := range m.store.Sets() {
			if port, running := m.runningPort(set); running {
				targets = append(targets, target{set, port})
			}
		}
	}

	// Наборы опрашиваются параллельно: по два секунды таймаута на каждый,
	// последовательно это до десяти секунд на запрос панели при пяти наборах.
	snaps := make([]stats.Snapshot, len(targets))
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	slots := make(chan struct{}, callsConcurrency)
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			snaps[i], errs[i] = m.fetchSetStats(req.r.Context(), t.set, t.port)
		}(i, t)
	}
	wg.Wait()

	for i, t := range targets {
		snap, err := snaps[i], errs[i]
		if err != nil {
			out.Unreachable = append(out.Unreachable, t.set.ID)
			continue
		}
		// Вес схем — свойство одного набора: в объединённом ответе молчим.
		if id != "" && snap.Schema != nil {
			out.Schema = snap.Schema
		}
		for _, c := range snap.Tail {
			row := callRow{Time: c.Time, Client: c.Client, Server: c.Server, Tool: c.Tool, MS: c.Millis, Failed: c.Failed, Error: c.Message}
			if id == "" {
				row.Set = t.set.ID
			}
			out.Calls = append(out.Calls, row)
		}
		for _, c := range snap.Counters {
			row := counterRow{
				Client: c.Client, Server: c.Server, Tool: c.Tool,
				Calls: c.Calls, Failures: c.Failures, TotalMs: c.TotalMs, P50Ms: c.P50Ms, P95Ms: c.P95Ms,
			}
			if id == "" {
				row.Set = t.set.ID
			}
			out.Counters = append(out.Counters, row)
		}
	}

	sort.SliceStable(out.Calls, func(i, j int) bool { return out.Calls[i].Time.After(out.Calls[j].Time) })
	if len(out.Calls) > limit {
		out.Calls = out.Calls[:limit]
	}
	writeJSON(req.w, http.StatusOK, out)
	return nil
}

// runningPort — порт работающего набора; остановленный набор не опрашиваем.
func (m *Manager) runningPort(set *registry.Set) (int, bool) {
	st := m.rt.status(set)
	if !st.Running || st.Port == 0 {
		return 0, false
	}
	return st.Port, true
}

// fetchSetStats читает /stats набора: у открытого набора токена нет, поэтому
// запрос идёт без заголовка и набор сам отдаёт редактированный снимок.
func (m *Manager) fetchSetStats(ctx context.Context, set *registry.Set, port int) (stats.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, callsTimeout)
	defer cancel()

	addr := net.JoinHostPort(m.rt.probeAddr, strconv.Itoa(port))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/stats", nil)
	if err != nil {
		return stats.Snapshot{}, err
	}
	if set.Token != "" {
		req.Header.Set("Authorization", "Bearer "+set.Token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return stats.Snapshot{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return stats.Snapshot{}, fmt.Errorf("набор %s: /stats вернул %d", set.ID, res.StatusCode)
	}
	var snap stats.Snapshot
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&snap); err != nil {
		return stats.Snapshot{}, err
	}
	return snap, nil
}

func callsLimit(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return callsDefaultLimit
	}
	if n > callsMaxLimit {
		return callsMaxLimit
	}
	return n
}
