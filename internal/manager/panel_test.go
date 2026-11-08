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
