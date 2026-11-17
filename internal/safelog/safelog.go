// Package safelog готовит значения для журнала: строка из запроса или конфига
// не должна нести перевод строки — иначе ею подделывают запись лога.
package safelog

import (
	"strings"
	"unicode"
)

// Value возвращает s без управляющих символов: \r, \n, \t и прочие C0/C1
// вырезаются, остальной текст остаётся как был.
func Value(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
