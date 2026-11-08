package manager

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/sintez/mcprig/web"
)

// panelCSP: панель живёт внешними app.css/app.js, inline-скриптов и стилей нет.
const panelCSP = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

var panelFS = web.FS()

var panelTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".svg":  "image/svg+xml",
	".json": "application/json; charset=utf-8",
}

// servePanel отдаёт статику панели из бинаря. Вызывается до auth только для
// GET/HEAD: содержимое одно и то же для всех, данные приходят из /api.
func (m *Manager) servePanel(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	if !strings.HasPrefix(name, "mock/") && strings.Contains(name, "/") {
		return false
	}
	body, err := fs.ReadFile(panelFS, name)
	if err != nil {
		return false
	}
	ext := path.Ext(name)
	typ, ok := panelTypes[ext]
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", typ)
	if ext == ".html" {
		w.Header().Set("Content-Security-Policy", panelCSP)
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = w.Write(body)
	return true
}
