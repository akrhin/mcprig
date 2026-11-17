package web

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestPanelIdsExist: каждый id, который app.js берёт через $('…'), обязан быть
// в index.html (или создаваться самим скриптом). Опечатка в id раньше всплывала
// только живым кликом.
func TestPanelIdsExist(t *testing.T) {
	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, m := range regexp.MustCompile(`id="([A-Za-z0-9_-]+)"`).FindAllStringSubmatch(string(html), -1) {
		present[m[1]] = true
	}
	// id, которые скрипт ставит сам
	for _, m := range regexp.MustCompile(`\.id = '([A-Za-z0-9_-]+)'`).FindAllStringSubmatch(string(js), -1) {
		present[m[1]] = true
	}

	used := map[string]bool{}
	for _, m := range regexp.MustCompile(`\$\('([A-Za-z0-9_-]+)'\)`).FindAllStringSubmatch(string(js), -1) {
		used[m[1]] = true
	}

	var missing []string
	for id := range used {
		if !present[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("app.js берёт id, которых нет в index.html: %s", strings.Join(missing, ", "))
	}
}
