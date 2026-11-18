package config

import (
	"slices"
	"strings"
	"testing"
)

// Токен под «не секретным» именем тоже маскируется: фильтра по имени поля нет,
// иначе GITHUB_PAT уходил бы наружу целиком.
func TestSecretsMasksEveryValue(t *testing.T) {
	const pat = "0123456789abcdef0123456789abcdef"
	srv := &Server{
		Env:     map[string]string{"GITHUB_PAT": pat, "SHORT": "1234567"},
		Headers: map[string]string{"Authorization": "Bearer abcdefgh"},
	}
	got := srv.Secrets()
	if !slices.Contains(got, pat) {
		t.Fatalf("GITHUB_PAT не попал в секреты: %v", got)
	}
	if !slices.Contains(got, "abcdefgh") {
		t.Fatalf("Bearer снят неверно: %v", got)
	}
	if slices.Contains(got, "Bearer abcdefgh") {
		t.Fatalf("префикс Bearer остался в секрете: %v", got)
	}
	if slices.Contains(got, "1234567") {
		t.Fatalf("значение из 7 байт замаскировано: %v", got)
	}

	text := ScrubSecrets("ключ "+pat+" и abcdefgh, короткое 1234567", got)
	if text != "ключ *** и ***, короткое 1234567" {
		t.Fatalf("маска: %q", text)
	}
	if !strings.Contains(text, "***") {
		t.Fatal("секрет остался в тексте")
	}
}
