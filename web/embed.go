// Package web carries the panel's static assets inside the binary: the panel
// must run from a single file, without a checkout next to it.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.css themes.css win98.css app.js theme.js favicon.svg mock/*.json
var assets embed.FS

// FS отдаёт встроенную статику целиком, вместе с mock/: при ?mock=1 панель
// читает эти файлы как ответы /api (см. servePanel, который пускает mock/
// вглубь каталога).
func FS() fs.FS {
	sub, err := fs.Sub(assets, ".")
	if err != nil {
		panic(err)
	}
	return sub
}
