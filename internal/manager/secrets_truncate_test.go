package manager

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A byte cut in the middle of a rune leaves invalid UTF-8, and the panel would
// show a replacement character instead of the tail of the message.
func TestTruncateKeepsRunes(t *testing.T) {
	s := strings.Repeat("я", 10) // 2 bytes each
	got := truncate(s, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("обрезанный текст не UTF-8: %q", got)
	}
	if len(got) > 5+len("…") {
		t.Fatalf("длина %d больше потолка %d + многоточие", len(got), 5)
	}
	if want := "яя…"; got != want {
		t.Fatalf("получилось %q, ждали %q", got, want)
	}
}

func TestTruncateLeavesShortAndEmptyAlone(t *testing.T) {
	if got := truncate("", 8); got != "" {
		t.Fatalf("пустая строка изменилась: %q", got)
	}
	// n is a byte budget; "привет" is 12 bytes, so 12 already fits.
	if got := truncate("привет", 12); got != "привет" {
		t.Fatalf("строка короче потолка изменилась: %q", got)
	}
	if got := truncate("привет", 20); got != "привет" {
		t.Fatalf("строка короче потолка изменилась: %q", got)
	}
}
