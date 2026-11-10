package manager

// История вызовов на диске: разрез по периодам для панели.

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sintez/mcprig/internal/stats"
)

// statsPeriods — допустимые значения period.
var statsPeriods = map[string]bool{
	stats.PeriodToday:   true,
	stats.Period7d:      true,
	stats.Period30d:     true,
	stats.PeriodArchive: true,
}

// apiStats отвечает разрезом истории вызовов одного набора или всех сразу.
// Каталог удалённого или ещё не работавшего набора — пустой ответ с
// unreachable, как у /api/calls.
func (m *Manager) apiStats(req *request) error {
	q := req.r.URL.Query()
	period := q.Get("period")
	if period == "" {
		period = stats.Period7d
	}
	if !statsPeriods[period] {
		return badRequest("period: today, 7d, 30d или archive")
	}

	id := q.Get("set")
	dirs := []string{}
	unreachable := []string{}
	if id != "" && id != "all" {
		if m.store.Set(id) == nil {
			return notFound("набор не найден")
		}
		if dir := m.statsDir(id); dirExists(dir) {
			dirs = append(dirs, dir)
		} else {
			unreachable = append(unreachable, id)
		}
	} else {
		for _, set := range m.store.Sets() {
			if dir := m.statsDir(set.ID); dirExists(dir) {
				dirs = append(dirs, dir)
			} else {
				// Документ обещает назвать набор без каталога и здесь.
				unreachable = append(unreachable, set.ID)
			}
		}
	}

	bucket := q.Get("bucket")
	if bucket != "" && bucket != "hour" && bucket != "day" && bucket != "month" {
		return badRequest("bucket: hour, day или month")
	}

	res := stats.QueryHistory(dirs, stats.Query{
		Period: period,
		Bucket: bucket,
		Server: q.Get("server"),
		Tool:   q.Get("tool"),
		Client: q.Get("client"),
	}, time.Now())
	res.Unreachable = unreachable
	writeJSON(req.w, http.StatusOK, res)
	return nil
}

// statsDir — каталог истории набора: его пишет сам процесс набора рядом со
// своим конфигом.
func (m *Manager) statsDir(id string) string {
	return filepath.Join(m.rt.dataDir, "sets", id, "stats")
}

func dirExists(dir string) bool {
	_, err := os.Stat(dir)
	return err == nil
}
