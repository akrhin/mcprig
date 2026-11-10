package manager

import (
	"net/http"
	"strings"
	"testing"
)

// Панель отдаётся из embed: без неё `/` — 404 и оператор видит пустой экран.
func TestPanelIsServed(t *testing.T) {
	e := newEnv(t, AuthOptions{Off: true})

	root := e.do(t, http.MethodGet, "/", nil)
	if root.Code != http.StatusOK {
		t.Fatalf("/ = %d, ожидалось 200", root.Code)
	}
	if ct := root.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !strings.Contains(root.Body.String(), "mcprig") {
		t.Fatalf("index.html без имени проекта")
	}
	if root.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("нет CSP у index.html")
	}

	css := e.do(t, http.MethodGet, "/app.css", nil)
	if css.Code != http.StatusOK || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("/app.css = %d %q", css.Code, css.Header().Get("Content-Type"))
	}

	// Остальные файлы панели лежат в том же embed: без них страница пустая.
	for _, asset := range []string{"/app.js", "/theme.js", "/themes.css"} {
		w := e.do(t, http.MethodGet, asset, nil)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("%s = %d, тело %d байт", asset, w.Code, w.Body.Len())
		}
	}
	if js := e.do(t, http.MethodGet, "/app.js", nil); !strings.Contains(js.Body.String(), "/api/stats") {
		t.Fatalf("/app.js не знает про /api/stats")
	}

	// /ui был единственной страницей раньше: старые закладки живут.
	ui := e.do(t, http.MethodGet, "/ui", nil)
	if ui.Code != http.StatusFound || ui.Header().Get("Location") != "/" {
		t.Fatalf("/ui = %d → %q, ожидался 302 на /", ui.Code, ui.Header().Get("Location"))
	}

	// Логин-страница отключена при Off — не притворяется панелью.
	if page := e.do(t, http.MethodGet, "/login", nil); page.Code != http.StatusNotFound {
		t.Fatalf("/login = %d, ожидалось 404 при MCPRIG_AUTH=off", page.Code)
	}
}
