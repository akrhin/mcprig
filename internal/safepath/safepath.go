// Package safepath — правила для путей, собранных из значений запроса и
// конфига. Живёт отдельным пакетом, потому что проверять обязаны и реестр, и
// менеджер, и читатель истории, а зависимостей у правила нет: цикла импортов
// не будет ни при каком порядке.
package safepath

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// segment — годное имя набора, сервера или каталога: то же правило, что у
// реестра (`internal/registry/validate.go`).
var segment = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Name сообщает, годится ли s в один сегмент пути: имя набора, сервера или
// каталога. Всё остальное — включая пустую строку, «/» и «..» — не сегмент.
func Name(s string) bool { return segment.MatchString(s) }

// Path сообщает, годится ли p как путь чтения: абсолютный, без «..» и без
// управляющих символов. Проверка стоит до filepath.Join: после него Clean уже
// свернул бы обход, и проверять стало бы нечего.
func Path(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	for _, part := range strings.Split(p, string(filepath.Separator)) {
		if part == ".." || strings.ContainsFunc(part, unicode.IsControl) {
			return false
		}
	}
	return true
}
