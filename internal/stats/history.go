package stats

// История вызовов на диске.
//
// Счётчики Recorder живут в памяти процесса: перезапуск набора их обнуляет, а
// панель спрашивает «что было вчера». Поэтому каждый вызов дублируется в
// каталог stats рядом с конфигом набора:
//
//	raw-YYYY-MM-DD.jsonl   по строке на вызов, сегодня + RawDays
//	day-YYYY-MM-DD.json    суточная агрегация, до DayDays
//	month-YYYY-MM.json     месяцы старше DayDays, хранятся всегда
//
// Запись идёт через канал: вызов инструмента не ждёт диска, а переполненный
// канал теряет строку и считает её в dropped.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sintez/mcprig/internal/atomicfile"
)

const (
	// RawDays — сколько суток хранится сырой журнал: сегодня плюс RawDays.
	RawDays = 7
	// DayDays — сколько суток живёт суточная агрегация; старше — только месяц.
	DayDays = 30

	// queueSize — длина канала записи. Строка весит около 120 байт, канал
	// переживает всплеск, не блокируя вызов.
	queueSize = 1024
	// rotateEvery — период суточной ротации и уборки.
	rotateEvery = 24 * time.Hour

	dayLayout   = "2006-01-02"
	monthLayout = "2006-01"
)

// histBounds — верхние границы корзин гистограммы в миллисекундах. Последняя
// корзина открытая (+∞).
var histBounds = [...]int64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000}

// Hist — гистограмма латентности в фиксированных корзинах.
type Hist [len(histBounds) + 1]int64

// histIndex — корзина значения ms.
func histIndex(ms int64) int {
	for i, hi := range histBounds {
		if ms <= hi {
			return i
		}
	}
	return len(histBounds)
}

// histMid — середина корзины: латентность, которую гистограмма выдаёт за
// перцентиль. Открытая корзина считается как (10001, 20000).
func histMid(i int) int64 {
	if i >= len(histBounds) {
		lo := histBounds[len(histBounds)-1] + 1
		return lo + lo/2
	}
	var lo int64
	if i > 0 {
		lo = histBounds[i-1] + 1
	}
	return (lo + histBounds[i]) / 2
}

// Triple is one (client, server, tool) aggregate in a day or month file.
type Triple struct {
	Client   string `json:"client"`
	Server   string `json:"server"`
	Tool     string `json:"tool"`
	Calls    int64  `json:"calls"`
	Failures int64  `json:"failures"`
	TotalMs  int64  `json:"totalMs"`
	Hist     Hist   `json:"hist"`
	LastMs   int64  `json:"lastMs"`
}

// rawLine is one line of raw-YYYY-MM-DD.jsonl. Текста ошибки нет намеренно:
// он бывает большим и не нужен разрезу по периодам.
type rawLine struct {
	Time   time.Time `json:"time"`
	Client string    `json:"client"`
	Server string    `json:"server"`
	Tool   string    `json:"tool"`
	MS     int64     `json:"ms"`
	Failed bool      `json:"failed"`
}

type dayFile struct {
	Day     string   `json:"day"`
	Triples []Triple `json:"triples"`
}

// monthFile хранит вклад по дням, а не готовую сумму: повторная свёртка того
// же дня перезаписывает его, а не удваивает счётчики.
type monthFile struct {
	Month string              `json:"month"`
	Days  map[string][]Triple `json:"days"`
}

// History пишет историю в один каталог. Одна горутина пишет и раз в сутки
// применяет ретенцию; вызывающие только кладут строку в канал.
type History struct {
	dir     string
	ch      chan rawLine
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	dropped atomic.Int64
	log     *slog.Logger
}

// OpenHistory создаёт каталог истории и запускает писателя. Ротация идёт в
// его горутине: набор мог не работать сутки и дольше, но старт из-за уборки
// ждать не должен.
func OpenHistory(dir string, log *slog.Logger) (*History, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	h := &History{
		dir:  dir,
		ch:   make(chan rawLine, queueSize),
		stop: make(chan struct{}),
		done: make(chan struct{}),
		log:  log,
	}
	go h.run()
	return h, nil
}

// Record queues one finished call. A full queue drops the line and counts it:
// the tool call never waits for the disk.
func (h *History) Record(client, server, tool string, ms int64, failed bool) {
	select {
	case h.ch <- rawLine{Time: time.Now(), Client: client, Server: server, Tool: tool, MS: ms, Failed: failed}:
	default:
		h.dropped.Add(1)
	}
}

// Dropped reports lines lost to a full queue since start.
func (h *History) Dropped() int64 { return h.dropped.Load() }

// Close drains the queue and stops the writer.
func (h *History) Close() error {
	h.once.Do(func() { close(h.stop) })
	<-h.done
	return nil
}

func (h *History) run() {
	defer close(h.done)
	if err := Rotate(h.dir, time.Now()); err != nil && h.log != nil {
		h.log.Warn("stats: ротация", "dir", h.dir, "err", err)
	}
	ticker := time.NewTicker(rotateEvery)
	defer ticker.Stop()
	w := &rawWriter{dir: h.dir}
	defer w.close()

	for {
		select {
		case l := <-h.ch:
			if err := w.write(l); err != nil && h.log != nil {
				h.log.Warn("stats: запись", "err", err)
			}
		case <-ticker.C:
			w.close()
			if err := Rotate(h.dir, time.Now()); err != nil && h.log != nil {
				h.log.Warn("stats: ротация", "err", err)
			}
		case <-h.stop:
			// Строки, уже принятые в канал, не теряются при закрытии.
			for {
				select {
				case l := <-h.ch:
					_ = w.write(l)
				default:
					return
				}
			}
		}
	}
}

// rawWriter appends lines to the journal of the line's own day.
type rawWriter struct {
	dir  string
	file *os.File
	day  string
}

func (w *rawWriter) write(l rawLine) error {
	day := l.Time.Format(dayLayout)
	if w.file == nil || w.day != day {
		w.close()
		f, err := os.OpenFile(filepath.Join(w.dir, "raw-"+day+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		w.file, w.day = f, day
	}
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = w.file.Write(append(b, '\n'))
	return err
}

func (w *rawWriter) close() {
	if w.file != nil {
		_ = w.file.Close()
	}
	w.file, w.day = nil, ""
}

// Rotate применяет ретенцию на момент now и идемпотентен: законченные сутки
// получают day-файл из сырого журнала, дни старше DayDays уходят в месяц,
// сырой журнал старше RawDays удаляется.
func Rotate(dir string, now time.Time) error {
	today := startOfDay(now)
	raws, _ := filepath.Glob(filepath.Join(dir, "raw-*.jsonl"))

	for _, p := range raws {
		day, ok := dayOf(p, "raw-", ".jsonl")
		if !ok || !day.Before(today) {
			continue
		}
		out := filepath.Join(dir, "day-"+day.Format(dayLayout)+".json")
		if _, err := os.Stat(out); err == nil {
			continue
		}
		triples, err := readRawTriples(p)
		if err != nil {
			return err
		}
		if err := writeJSONFile(out, dayFile{Day: day.Format(dayLayout), Triples: triples}); err != nil {
			return err
		}
	}

	cutoff := today.AddDate(0, 0, -(DayDays - 1))
	days, _ := filepath.Glob(filepath.Join(dir, "day-*.json"))
	for _, p := range days {
		day, ok := dayOf(p, "day-", ".json")
		if !ok || !day.Before(cutoff) {
			continue
		}
		df, err := readDayFile(p)
		if err != nil {
			return err
		}
		if err := foldMonth(dir, day, df.Triples); err != nil {
			return err
		}
		_ = os.Remove(p)
	}

	rawCutoff := today.AddDate(0, 0, -RawDays)
	for _, p := range raws {
		if day, ok := dayOf(p, "raw-", ".jsonl"); ok && day.Before(rawCutoff) {
			_ = os.Remove(p)
		}
	}
	return nil
}

// scanRaw reads a journal line by line. A broken line is skipped: a process
// killed mid-write leaves a torn last line, and that must not stop the fold
// or the query.
func scanRaw(path string, fn func(rawLine)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var l rawLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		fn(l)
	}
	return sc.Err()
}

// readRawTriples folds one journal into per-triple aggregates.
func readRawTriples(path string) ([]Triple, error) {
	acc := map[[3]string]*Triple{}
	err := scanRaw(path, func(l rawLine) {
		k := [3]string{l.Client, l.Server, l.Tool}
		t := acc[k]
		if t == nil {
			t = &Triple{Client: l.Client, Server: l.Server, Tool: l.Tool}
			acc[k] = t
		}
		t.Calls++
		t.TotalMs += l.MS
		t.LastMs = l.MS
		if l.Failed {
			t.Failures++
		}
		t.Hist[histIndex(l.MS)]++
	})
	if err != nil {
		return nil, err
	}
	out := make([]Triple, 0, len(acc))
	for _, t := range acc {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		if out[i].Client != out[j].Client {
			return out[i].Client < out[j].Client
		}
		return out[i].Tool < out[j].Tool
	})
	return out, nil
}

func readDayFile(path string) (dayFile, error) {
	var df dayFile
	b, err := os.ReadFile(path)
	if err != nil {
		return df, err
	}
	if err := json.Unmarshal(b, &df); err != nil {
		return df, fmt.Errorf("%s: %w", path, err)
	}
	return df, nil
}

func writeJSONFile(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(b, '\n'))
}

// foldMonth merges one day into its month file. A broken month file is an
// error, not a silent reset: the day stays on disk until the merge succeeds.
func foldMonth(dir string, day time.Time, triples []Triple) error {
	path := filepath.Join(dir, "month-"+day.Format(monthLayout)+".json")
	mf := monthFile{Month: day.Format(monthLayout), Days: map[string][]Triple{}}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &mf); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if mf.Days == nil {
			mf.Days = map[string][]Triple{}
		}
	case !os.IsNotExist(err):
		return err
	}
	mf.Days[day.Format(dayLayout)] = triples
	return writeJSONFile(path, mf)
}

func dayOf(path, prefix, suffix string) (time.Time, bool) {
	base := filepath.Base(path)
	if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, suffix) {
		return time.Time{}, false
	}
	d, err := time.ParseInLocation(dayLayout, strings.TrimSuffix(strings.TrimPrefix(base, prefix), suffix), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return d, true
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
