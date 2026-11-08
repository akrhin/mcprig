package manager

import (
	"html/template"
	"net/http"
)

const contentSecurityPolicy = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

func setHTMLHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
}

func writeHTML(w http.ResponseWriter, status int, tmpl *template.Template, data any) {
	setHTMLHeaders(w)
	w.WriteHeader(status)
	_ = tmpl.Execute(w, data)
}

type uiData struct {
	Version string
	Servers int
	Sets    []setView
}

func (m *Manager) pageIndex(req *request) error {
	data := uiData{Version: m.version, Servers: len(m.store.Servers()), Sets: m.setViews()}
	writeHTML(req.w, http.StatusOK, uiTemplate, data)
	return nil
}

// Страница минимальная: панель — этап 5. Никакого inline-JS, только текст.
var uiTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mcprig</title>
<style>
 body { font: 15px/1.5 system-ui, sans-serif; margin: 2rem auto; max-width: 60rem; padding: 0 1rem; color: #1b1b1b; }
 table { border-collapse: collapse; width: 100%; margin-top: 1rem; }
 th, td { text-align: left; padding: .4rem .6rem; border-bottom: 1px solid #ddd; }
 code { background: #f3f3f3; padding: 0 .3rem; border-radius: 3px; }
 .up { color: #0a7d2b; } .down { color: #9b1c1c; }
</style>
</head>
<body>
<h1>mcprig</h1>
<p>Версия {{.Version}} · серверов в реестре: {{.Servers}}</p>
<table>
<thead><tr><th>набор</th><th>порт</th><th>состояние</th><th>ключ</th><th>серверы</th></tr></thead>
<tbody>
{{range .Sets}}<tr>
<td><code>{{.ID}}</code>{{if .Description}} — {{.Description}}{{end}}</td>
<td>{{.Port}}</td>
<td>{{if .Runtime.Running}}<span class="up">работает</span>{{else}}<span class="down">остановлен</span>{{end}}</td>
<td>{{if .KeyPrefix}}<code>{{.KeyPrefix}}…</code>{{else}}нет{{end}}</td>
<td>{{len .Servers}}</td>
</tr>{{else}}<tr><td colspan="5">наборов нет</td></tr>{{end}}
</tbody>
</table>
<p>API: <code>/api/state</code>, <code>/api/sets</code>, <code>/api/servers</code>, <code>/api/env</code></p>
</body>
</html>
`))

type loginData struct {
	Error string
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mcprig — вход</title>
<style>
 body { font: 15px/1.5 system-ui, sans-serif; margin: 4rem auto; max-width: 22rem; padding: 0 1rem; }
 label { display: block; margin: .6rem 0 .2rem; }
 input { width: 100%; padding: .45rem; box-sizing: border-box; }
 button { margin-top: 1rem; padding: .5rem 1rem; }
 .err { color: #9b1c1c; }
</style>
</head>
<body>
<h1>mcprig</h1>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post" action="/api/login">
<label for="user">Логин</label>
<input id="user" name="user" autocomplete="username" autofocus>
<label for="password">Пароль</label>
<input id="password" name="password" type="password" autocomplete="current-password">
<button type="submit">Войти</button>
</form>
</body>
</html>
`))
