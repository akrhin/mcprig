package stats

import (
	"testing"
	"time"
)

// Потоковый разрез 7d держит в памяти распределение латентностей, а не строки
// журнала: процентили обязаны остаться точными, поэтому эталон — прежний путь
// quantile по полному списку значений.
func TestQuery7dMatchesExactQuantiles(t *testing.T) {
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, time.Local)
	today := startOfDay(now)
	dir := t.TempDir()

	lines := make([]rawLine, 0, 2016)
	for i := 0; i < 7; i++ {
		day := today.AddDate(0, 0, -i)
		dayLines := make([]rawLine, 0, 288)
		for j := 0; j < 288; j++ {
			n := i*288 + j
			dayLines = append(dayLines, rawLine{
				Time:   day.Add(time.Duration(j) * 5 * time.Minute),
				Client: []string{"opencode", "claude"}[n%2],
				Server: []string{"alpha", "beta"}[(n/3)%2],
				Tool:   []string{"echo", "fetch"}[(n/7)%2],
				// Значения редкие и с повторами: процентиль обязан совпасть
				// поимённо, а не попасть в середину корзины.
				MS:     int64(n*37%1000) + int64(n%3),
				Failed: n%11 == 0,
			})
		}
		lines = append(lines, dayLines...)
		writeRaw(t, dir, day, dayLines)
	}

	res := QueryHistory([]string{dir}, Query{Period: Period7d}, now)
	if res.Approximate {
		t.Fatal("7d не должен быть приблизительным")
	}

	var all []int64
	byRow := map[[2]string][]int64{}
	for _, l := range lines {
		all = append(all, l.MS)
		k := [2]string{l.Server, l.Tool}
		byRow[k] = append(byRow[k], l.MS)
	}
	if want := int64(len(all)); res.Totals.Calls != want {
		t.Fatalf("calls = %d, ожидалось %d", res.Totals.Calls, want)
	}
	if got, want := res.Totals.P50Ms, quantile(all, 0.50); got != want {
		t.Errorf("p50 = %d, эталон %d", got, want)
	}
	if got, want := res.Totals.P95Ms, quantile(all, 0.95); got != want {
		t.Errorf("p95 = %d, эталон %d", got, want)
	}
	for _, row := range res.Tools {
		vals := byRow[[2]string{row.Server, row.Tool}]
		if got, want := row.P50Ms, quantile(vals, 0.50); got != want {
			t.Errorf("%s/%s p50 = %d, эталон %d", row.Server, row.Tool, got, want)
		}
		if got, want := row.P95Ms, quantile(vals, 0.95); got != want {
			t.Errorf("%s/%s p95 = %d, эталон %d", row.Server, row.Tool, got, want)
		}
	}
	if len(res.Series) != 7*24 {
		t.Fatalf("ряд = %d точек, ожидалось 168", len(res.Series))
	}
}

// Память аккумулятора — O(различных латентностей), а не O(строк): сто тысяч
// вызовов с тремя десятками разных значений не должны оставить сто тысяч
// записей.
func TestAccKeepsDistributionNotRows(t *testing.T) {
	a := &acc{exact: map[int64]int64{}}
	all := make([]int64, 0, 100000)
	for i := 0; i < 100000; i++ {
		all = append(all, int64(i%37))
		a.addCall(all[i], false)
	}
	if a.calls != 100000 {
		t.Fatalf("calls = %d", a.calls)
	}
	if len(a.exact) != 37 {
		t.Fatalf("записей в распределении %d, ожидалось 37", len(a.exact))
	}
	p50, p95 := a.percentiles(false)
	if p50 != quantile(all, 0.50) || p95 != quantile(all, 0.95) {
		t.Fatalf("процентили %d/%d, эталон %d/%d", p50, p95, quantile(all, 0.50), quantile(all, 0.95))
	}
}

// Приблизительный режим не платит за точность вовсе: распределение не ведётся.
func TestApproximateBundleSkipsDistribution(t *testing.T) {
	b := newBundle(stepDay, true, Query{})
	b.addRaw("", rawLine{Time: time.Now(), Client: "c", Server: "s", Tool: "t", MS: 5})
	if b.totals.exact != nil {
		t.Fatal("приблизительный разрез ведёт распределение латентностей")
	}
	if b.totals.calls != 1 || b.totals.totalMs != 5 {
		t.Fatalf("totals = %+v", b.totals)
	}
}
