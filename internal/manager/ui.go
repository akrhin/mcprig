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
