package manager

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/stats"
)

// TestCallsPagination — окно before/after режет хвост по времени строки:
// две страницы не пересекаются и не теряют строк, after оставляет только
// более новые, битое время — 400.
func TestCallsPagination(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fs")
	id := e.addSetBody(t, map[string]any{"name": "paged", "servers": []string{"fs"}, "open": true})

	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	// Фикстура — 5 вызовов с шагом в секунду, в хвосте набора новее-первым.
	tail := make([]stats.Call, 0, 5)
	for i := 5; i >= 1; i-- {
		tail = append(tail, stats.Call{
			Time: base.Add(time.Duration(i) * time.Second), Client: stats.AnonymousLabel,
			Server: "fs", Tool: "echo", Millis: int64(i),
		})
	}
	srv, _ := fakeStats(t, stats.Snapshot{Set: id, Tail: tail})
	markRunning(e.m, id, serverPort(t, srv))

	at := func(i int) string {
		return url.QueryEscape(base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano))
	}
	page := func(q string) (callsBody, string) {
		w := e.do(t, http.MethodGet, "/api/calls?set="+id+q, nil)
		if w.Code != http.StatusOK {
			return callsBody{}, w.Body.String()
		}
		return decode[callsBody](t, w), ""
	}
	times := func(b callsBody) []string {
		out := make([]string, 0, len(b.Calls))
		for _, c := range b.Calls {
			out = append(out, c.Time)
		}
		return out
	}
	want := func(b callsBody, secs ...int) {
		t.Helper()
		got := times(b)
		if len(got) != len(secs) {
			t.Fatalf("строк = %d (%v), ожидалось %d (%v)", len(got), got, len(secs), secs)
		}
		for i, s := range secs {
			if got[i] != base.Add(time.Duration(s)*time.Second).Format(time.RFC3339Nano) {
				t.Fatalf("строка %d = %s, ожидалась +%d с: %v", i, got[i], s, got)
			}
		}
	}

	// Первая страница — две самые новые строки.
	first, errBody := page("&limit=2")
	if errBody != "" {
		t.Fatalf("страница 1: %s", errBody)
	}
	want(first, 5, 4)

	// Вторая и третья страницы идут от времени последней строки предыдущей:
	// пересечений нет, хвост выбран целиком.
	second, errBody := page("&limit=2&before=" + at(4))
	if errBody != "" {
		t.Fatalf("страница 2: %s", errBody)
	}
	want(second, 3, 2)
	third, errBody := page("&before=" + at(2))
	if errBody != "" {
		t.Fatalf("страница 3: %s", errBody)
	}
	want(third, 1)

	seen := map[string]bool{}
	for _, b := range []callsBody{first, second, third} {
		for _, ts := range times(b) {
			if seen[ts] {
				t.Fatalf("строка %s пришла дважды: страницы пересекаются", ts)
			}
			seen[ts] = true
		}
	}
	if len(seen) != 5 {
		t.Fatalf("страницы покрыли %d строк из 5: %v", len(seen), seen)
	}

	// before за пределы хвоста — честно пусто, а не старые строки.
	empty, errBody := page("&before=" + at(0))
	if errBody != "" {
		t.Fatalf("before за хвостом: %s", errBody)
	}
	want(empty)

	// after — строго позже: сама граница в ответ не входит.
	newer, errBody := page("&after=" + at(3))
	if errBody != "" {
		t.Fatalf("after: %s", errBody)
	}
	want(newer, 5, 4)

	// Дробная часть времени (RFC3339Nano) разбирается.
	frac, errBody := page("&before=" + url.QueryEscape(base.Add(3500*time.Millisecond).Format(time.RFC3339Nano)))
	if errBody != "" {
		t.Fatalf("before с наносекундами: %s", errBody)
	}
	want(frac, 3, 2, 1)

	// limit действует вместе с окном.
	capped, errBody := page("&limit=1&before=" + at(5))
	if errBody != "" {
		t.Fatalf("limit с before: %s", errBody)
	}
	want(capped, 4)

	// Битое время и оба окна сразу — 400 с указанием параметра.
	bad := []struct{ q, param string }{
		{"&before=вчера", "before"},
		{"&after=2026-11-08", "after"},
		{"&before=" + at(4) + "&after=" + at(1), "before"},
	}
	for _, c := range bad {
		w := e.do(t, http.MethodGet, "/api/calls?set="+id+c.q, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: код %d, ожидался 400: %s", c.q, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), c.param) {
			t.Fatalf("%s: текст ошибки без имени параметра: %s", c.q, w.Body.String())
		}
	}
}

// TestCallsPaginationEqualTime — известное ограничение строгого before: граница
// сравнивает только время, поэтому из двух вызовов с одинаковой меткой на
// границе страницы «соседний» теряется. Дублей при этом нет, и тест падает,
// если граница станет нестрогой и потерянная строка вернётся дублем.
func TestCallsPaginationEqualTime(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})
	e.addServer(t, "fs")
	id := e.addSetBody(t, map[string]any{"name": "tie", "servers": []string{"fs"}, "open": true})

	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	tie := base.Add(3 * time.Second)
	// echo-twin-a и echo-twin-b — одна и та же метка времени на границе.
	srv, _ := fakeStats(t, stats.Snapshot{Set: id, Tail: []stats.Call{
		{Time: base.Add(5 * time.Second), Client: stats.AnonymousLabel, Server: "fs", Tool: "echo-five"},
		{Time: tie, Client: stats.AnonymousLabel, Server: "fs", Tool: "echo-twin-a"},
		{Time: tie, Client: stats.AnonymousLabel, Server: "fs", Tool: "echo-twin-b"},
		{Time: base.Add(time.Second), Client: stats.AnonymousLabel, Server: "fs", Tool: "echo-one"},
	}})
	markRunning(e.m, id, serverPort(t, srv))

	page := func(q string) callsBody {
		t.Helper()
		w := e.do(t, http.MethodGet, "/api/calls?set="+id+q, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: код %d: %s", q, w.Code, w.Body.String())
		}
		return decode[callsBody](t, w)
	}
	tools := func(b callsBody) []string {
		out := make([]string, 0, len(b.Calls))
		for _, c := range b.Calls {
			out = append(out, c.Tool)
		}
		return out
	}
	atTie := url.QueryEscape(tie.Format(time.RFC3339Nano))

	// Клиент листает по одной строке, before — время последней строки страницы.
	first := page("&limit=1")
	if got := tools(first); len(got) != 1 || got[0] != "echo-five" {
		t.Fatalf("страница 1 = %v, ожидался echo-five", got)
	}
	second := page("&limit=1&before=" + url.QueryEscape(base.Add(5*time.Second).Format(time.RFC3339Nano)))
	if got := tools(second); len(got) != 1 || got[0] != "echo-twin-a" {
		t.Fatalf("страница 2 = %v, ожидался echo-twin-a", got)
	}
	third := page("&before=" + atTie)
	if got := tools(third); len(got) != 1 || got[0] != "echo-one" {
		t.Fatalf("страница 3 = %v, ожидался только echo-one: строгий before не пускает строки с Time == before", got)
	}

	// Ограничение названо явно: вторая строка с той же меткой не пришла никуда.
	// Стань граница нестрогой, echo-twin-b всплыл бы на странице 3 — и это был бы
	// дубль echo-twin-a со страницы 2, на чём проверка ниже и падает.
	all := map[string]bool{}
	for _, b := range []callsBody{first, second, third} {
		for _, tool := range tools(b) {
			if all[tool] {
				t.Fatalf("%s пришёл дважды: страницы дублируют строки", tool)
			}
			all[tool] = true
		}
	}
	if len(all) != 3 {
		t.Fatalf("страницы покрыли %d строк из 4: %v", len(all), all)
	}
	if !all["echo-twin-a"] {
		t.Fatalf("первая строка пары должна приходить: %v", all)
	}
	if all["echo-twin-b"] {
		t.Fatalf("вторая строка пары пришла — граница перестала быть строгой: %v", all)
	}
}
