package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRaw кладёт строки в сырой журнал указанного дня, как это делает
// процесс набора.
func writeRaw(t *testing.T, dir string, day time.Time, lines []rawLine) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "raw-"+day.Format(dayLayout)+".jsonl")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// Ротация собирает day-файл из сырого журнала закончившихся суток.
func TestRotateBuildsDayFileFromRaw(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	yesterday := startOfDay(now).AddDate(0, 0, -1)
	raw := writeRaw(t, dir, yesterday, []rawLine{
		{Time: yesterday.Add(2 * time.Hour), Client: "opencode", Server: "alpha", Tool: "echo", MS: 12},
		{Time: yesterday.Add(3 * time.Hour), Client: "opencode", Server: "alpha", Tool: "echo", MS: 40, Failed: true},
		{Time: yesterday.Add(4 * time.Hour), Client: "claude", Server: "beta", Tool: "fetch", MS: 700},
	})

	if err := Rotate(dir, now); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	df, err := readDayFile(filepath.Join(dir, "day-"+yesterday.Format(dayLayout)+".json"))
	if err != nil {
		t.Fatalf("day-файл: %v", err)
	}
	if len(df.Triples) != 2 {
		t.Fatalf("строк = %d, ожидалось 2: %+v", len(df.Triples), df.Triples)
	}
	echo := df.Triples[0]
	if echo.Server != "alpha" || echo.Tool != "echo" || echo.Client != "opencode" {
		t.Fatalf("порядок строк: %+v", df.Triples)
	}
	if echo.Calls != 2 || echo.Failures != 1 || echo.TotalMs != 52 || echo.LastMs != 40 {
		t.Fatalf("alpha/echo = %+v", echo)
	}
	if echo.Hist[histIndex(12)] != 1 || echo.Hist[histIndex(40)] != 1 {
		t.Fatalf("гистограмма: %v", echo.Hist)
	}
	// Корзины фиксированные: 12 мс — это ≤20, 40 мс — это ≤50.
	if echo.Hist[4] != 1 || echo.Hist[5] != 1 {
		t.Fatalf("корзины не совпали со схемой: %v", echo.Hist)
	}
	if _, err := os.Stat(raw); err != nil {
		t.Fatalf("сырой журнал вчерашних суток удалён: %v", err)
	}
	// Сегодняшних суток день ещё не закончился — day-файла быть не должно.
	if _, err := os.Stat(filepath.Join(dir, "day-"+startOfDay(now).Format(dayLayout)+".json")); err == nil {
		t.Fatal("day-файл собран для незакончившихся суток")
	}
}

// Ретенция: raw — сегодня + 7, day — 30, старше — только месяц.
func TestRotateRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	today := startOfDay(now)
	day10 := today.AddDate(0, 0, -10)
	day40 := today.AddDate(0, 0, -40)

	rawToday := writeRaw(t, dir, today, []rawLine{{Time: now.Add(-time.Minute), Client: "c", Server: "s", Tool: "t", MS: 5}})
	writeRaw(t, dir, day10, []rawLine{{Time: day10.Add(time.Hour), Client: "c", Server: "s", Tool: "t", MS: 30}})
	writeRaw(t, dir, day40, []rawLine{{Time: day40.Add(time.Hour), Client: "c", Server: "s", Tool: "t", MS: 300}})

	if err := Rotate(dir, now); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	if _, err := os.Stat(rawToday); err != nil {
		t.Fatalf("сырой журнал сегодняшних суток удалён: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "raw-"+day10.Format(dayLayout)+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("raw старше 7 суток остался: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "day-"+day10.Format(dayLayout)+".json")); err != nil {
		t.Fatalf("day-файл 10-суточной давности удалён: %v", err)
	}

	month := filepath.Join(dir, "month-"+day40.Format(monthLayout)+".json")
	if _, err := os.Stat(filepath.Join(dir, "day-"+day40.Format(dayLayout)+".json")); !os.IsNotExist(err) {
		t.Fatalf("day-файл 40-суточной давности остался: %v", err)
	}
	b, err := os.ReadFile(month)
	if err != nil {
		t.Fatalf("месячный файл: %v", err)
	}
	var mf monthFile
	if err := json.Unmarshal(b, &mf); err != nil {
		t.Fatal(err)
	}
	triples := mf.Days[day40.Format(dayLayout)]
	if len(triples) != 1 || triples[0].Calls != 1 || triples[0].TotalMs != 300 {
		t.Fatalf("свёртка в месяц: %+v", mf.Days)
	}

	// Повторная ротация не удваивает счётчики месяца.
	if err := Rotate(dir, now); err != nil {
		t.Fatalf("повторный Rotate: %v", err)
	}
	b, err = os.ReadFile(month)
	if err != nil {
		t.Fatal(err)
	}
	mf = monthFile{}
	if err := json.Unmarshal(b, &mf); err != nil {
		t.Fatal(err)
	}
	if got := mf.Days[day40.Format(dayLayout)]; len(got) != 1 || got[0].Calls != 1 {
		t.Fatalf("повторная свёртка удвоила день: %+v", got)
	}
}

// Период 7d считает процентили по сырым значениям точно.
func TestQuery7dExactPercentiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	base := startOfDay(now)
	lines := make([]rawLine, 0, 100)
	for i := 1; i <= 100; i++ {
		lines = append(lines, rawLine{
			Time: base.Add(time.Duration(i) * time.Minute), Client: "opencode",
			Server: "alpha", Tool: "echo", MS: int64(i),
		})
	}
	writeRaw(t, dir, base, lines)

	res := QueryHistory([]string{dir}, Query{Period: Period7d}, now)
	if res.Approximate {
		t.Fatal("7d должна быть точной")
	}
	if res.Totals.Calls != 100 || res.Totals.P50Ms != 50 || res.Totals.P95Ms != 95 {
		t.Fatalf("totals = %+v", res.Totals)
	}
	if len(res.Series) != 2 { // 100 минут с 00:00 — часы 00 и 01
		t.Fatalf("точек = %d: %+v", len(res.Series), res.Series)
	}
	if res.Series[0].T.Hour() != 0 || res.Series[0].Calls != 59 {
		t.Fatalf("первая точка = %+v", res.Series[0])
	}
	if len(res.Tools) != 1 || res.Tools[0].Server != "alpha" || res.Tools[0].Tool != "echo" {
		t.Fatalf("tools = %+v", res.Tools)
	}
	if len(res.Servers) != 1 || res.Servers[0].Server != "alpha" || len(res.Clients) != 1 {
		t.Fatalf("servers/clients = %+v / %+v", res.Servers, res.Clients)
	}
}

// Период 30d отвечает по гистограмме: процентиль — середина корзины.
func TestQuery30dApproximateFromHistogram(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	today := startOfDay(now)
	tenDaysAgo := today.AddDate(0, 0, -10)
	writeRaw(t, dir, tenDaysAgo, []rawLine{
		{Time: tenDaysAgo.Add(time.Hour), Client: "opencode", Server: "alpha", Tool: "echo", MS: 12},
	})
	writeRaw(t, dir, today, []rawLine{
		{Time: now.Add(-time.Minute), Client: "opencode", Server: "alpha", Tool: "echo", MS: 12},
	})
	if err := Rotate(dir, now); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	res := QueryHistory([]string{dir}, Query{Period: Period30d}, now)
	if !res.Approximate {
		t.Fatal("30d должна быть приблизительной")
	}
	if res.Totals.Calls != 2 {
		t.Fatalf("30d не добрала сырые сутки: %+v", res.Totals)
	}
	// 12 мс — корзина (11..20), середина 15.
	if res.Totals.P50Ms != 15 || res.Totals.P95Ms != 15 {
		t.Fatalf("процентили по гистограмме = %+v", res.Totals)
	}
}

// Архив читается из месячных файлов.
func TestQueryArchiveFromMonth(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	old := startOfDay(now).AddDate(0, 0, -40)
	writeRaw(t, dir, old, []rawLine{{Time: old.Add(time.Hour), Client: "c", Server: "s", Tool: "t", MS: 12, Failed: true}})
	if err := Rotate(dir, now); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	res := QueryHistory([]string{dir}, Query{Period: PeriodArchive}, now)
	if !res.Approximate || res.Totals.Calls != 1 || res.Totals.Failures != 1 {
		t.Fatalf("архив = %+v", res.Totals)
	}
	if len(res.Series) != 1 || res.Series[0].T.Month() != old.Month() {
		t.Fatalf("месячный ряд: %+v", res.Series)
	}
	if res.From.Equal(old) == false {
		t.Fatalf("from = %v, ожидалось %v", res.From, old)
	}
}

// set=all — это несколько каталогов в одном ответе.
func TestQueryMergesAllSets(t *testing.T) {
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	today := startOfDay(now)
	d1, d2 := t.TempDir(), t.TempDir()
	writeRaw(t, d1, today, []rawLine{{Time: now.Add(-time.Minute), Client: "opencode", Server: "a", Tool: "t", MS: 10}})
	writeRaw(t, d2, today, []rawLine{
		{Time: now.Add(-time.Minute), Client: "claude", Server: "b", Tool: "u", MS: 20, Failed: true},
		{Time: now.Add(-2 * time.Minute), Client: "claude", Server: "b", Tool: "u", MS: 30},
	})

	res := QueryHistory([]string{d1, d2}, Query{Period: PeriodToday}, now)
	if res.Totals.Calls != 3 || res.Totals.Failures != 1 {
		t.Fatalf("слияние: %+v", res.Totals)
	}
	if len(res.Clients) != 2 || len(res.Servers) != 2 || len(res.Tools) != 2 {
		t.Fatalf("разрезы: %+v %+v %+v", res.Clients, res.Servers, res.Tools)
	}
	if res.Totals.P95Ms != 20 { // 3 значения: индекс int(0.95*2) = 1
		t.Fatalf("p95 = %d, ожидалось 20", res.Totals.P95Ms)
	}
}

// Битая строка журнала не валит ни свёртку, ни запрос.
func TestBrokenJournalLinesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	today := startOfDay(now)
	p := writeRaw(t, dir, today, []rawLine{{Time: now.Add(-time.Minute), Client: "c", Server: "s", Tool: "t", MS: 10}})
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"time\":\"не дата\"\nне json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	res := QueryHistory([]string{dir}, Query{Period: PeriodToday}, now)
	if res.Totals.Calls != 1 {
		t.Fatalf("битые строки учтены: %+v", res.Totals)
	}
	if err := Rotate(dir, now.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("Rotate с битым журналом: %v", err)
	}
	df, err := readDayFile(filepath.Join(dir, "day-"+today.Format(dayLayout)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(df.Triples) != 1 || df.Triples[0].Calls != 1 {
		t.Fatalf("свёртка с битой строкой: %+v", df.Triples)
	}
}

// Файлы истории — 0600, каталог — 0700.
func TestHistoryFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	day := time.Now().Format(dayLayout)
	h, err := OpenHistory(dir, nil)
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	h.Record("opencode", "alpha", "echo", 12, false)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw := filepath.Join(dir, "raw-"+day+".jsonl")
	if info, err := os.Stat(raw); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("raw: %v, режим %v", err, info.Mode().Perm())
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("каталог: %v, режим %v", err, info.Mode().Perm())
	}

	if err := Rotate(dir, time.Now().AddDate(0, 0, 1)); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, "day-"+day+".json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("day: %v, режим %v", err, info.Mode().Perm())
	}
}

// Переполненный канал теряет строку и считает её, но не блокирует вызов.
func TestRecordDoesNotBlockWhenQueueIsFull(t *testing.T) {
	h := &History{ch: make(chan rawLine, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5; i++ {
			h.Record("c", "s", "t", int64(i), false)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record заблокировался на полном канале")
	}
	if got := h.Dropped(); got != 4 {
		t.Fatalf("dropped = %d, ожидалось 4", got)
	}
}

// Записанный вызов читается обратно из сырого журнала.
func TestRecordWritesRawLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stats")
	day := time.Now().Format(dayLayout)
	h, err := OpenHistory(dir, nil)
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	h.Record("opencode", "alpha", "echo", 12, true)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var lines []rawLine
	path := filepath.Join(dir, "raw-"+day+".jsonl")
	if err := scanRaw(path, func(l rawLine) { lines = append(lines, l) }); err != nil {
		t.Fatalf("scanRaw: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("строк = %d, ожидалась 1", len(lines))
	}
	l := lines[0]
	if l.Client != "opencode" || l.Server != "alpha" || l.Tool != "echo" || l.MS != 12 || !l.Failed {
		t.Fatalf("строка = %+v", l)
	}
}

// Фильтры server/tool/client действуют и на totals, и на разрезы: чужая
// тройка не попадает никуда.
func TestQueryTodayFilters(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	today := startOfDay(now)
	writeRaw(t, dir, today, []rawLine{
		{Time: now.Add(-time.Minute), Client: "opencode", Server: "alpha", Tool: "echo", MS: 10},
		{Time: now.Add(-2 * time.Minute), Client: "claude", Server: "beta", Tool: "fetch", MS: 20, Failed: true},
	})

	res := QueryHistory([]string{dir}, Query{Period: PeriodToday, Server: "alpha"}, now)
	if res.Totals.Calls != 1 || res.Totals.Failures != 0 || res.Totals.P95Ms != 10 {
		t.Fatalf("фильтр server: %+v", res.Totals)
	}
	if len(res.Servers) != 1 || res.Servers[0].Server != "alpha" || res.Servers[0].Calls != 1 {
		t.Fatalf("servers = %+v", res.Servers)
	}
	for _, row := range res.Tools {
		if row.Tool == "fetch" || row.Server == "beta" {
			t.Fatalf("в tools попал чужой сервер: %+v", row)
		}
	}
	for _, row := range res.Clients {
		if row.Client == "claude" {
			t.Fatalf("в clients попал чужой клиент: %+v", row)
		}
	}

	// Три фильтра вместе оставляют ровно одну тройку.
	res = QueryHistory([]string{dir}, Query{Period: PeriodToday, Server: "alpha", Tool: "echo", Client: "opencode"}, now)
	if res.Totals.Calls != 1 || len(res.Tools) != 1 || len(res.Clients) != 1 {
		t.Fatalf("фильтр тройки: %+v %+v %+v", res.Totals, res.Tools, res.Clients)
	}

	// bucket=day на сегодняшних сутках — одна точка.
	res = QueryHistory([]string{dir}, Query{Period: PeriodToday, Bucket: "day"}, now)
	if len(res.Series) != 1 || res.Series[0].Calls != 2 || !res.Series[0].T.Equal(today) {
		t.Fatalf("серия по дню: %+v", res.Series)
	}
}

// Два дня одного месяца живут в одном month-файле, и повторная ротация не
// удваивает ни один из них.
func TestRotateMonthKeepsTwoDays(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.Local)
	today := startOfDay(now)
	d1 := today.AddDate(0, 0, -51) // 20 сентября
	d2 := today.AddDate(0, 0, -50) // 21 сентября
	writeRaw(t, dir, d1, []rawLine{{Time: d1.Add(time.Hour), Client: "c", Server: "s", Tool: "t", MS: 10}})
	writeRaw(t, dir, d2, []rawLine{{Time: d2.Add(time.Hour), Client: "c", Server: "s", Tool: "t", MS: 20}})

	for i := 0; i < 2; i++ {
		if err := Rotate(dir, now); err != nil {
			t.Fatalf("Rotate #%d: %v", i+1, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "month-"+d1.Format(monthLayout)+".json"))
	if err != nil {
		t.Fatalf("месячный файл: %v", err)
	}
	var mf monthFile
	if err := json.Unmarshal(b, &mf); err != nil {
		t.Fatal(err)
	}
	if len(mf.Days) != 2 {
		t.Fatalf("дней в месяце = %d: %+v", len(mf.Days), mf.Days)
	}
	for _, day := range []time.Time{d1, d2} {
		got := mf.Days[day.Format(dayLayout)]
		if len(got) != 1 || got[0].Calls != 1 {
			t.Fatalf("%s: %+v", day.Format(dayLayout), got)
		}
	}
}
