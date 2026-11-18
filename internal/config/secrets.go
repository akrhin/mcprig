package config

import (
	"strings"
)

// Secrets собирает значения, которые нельзя выпускать наружу: все env и
// заголовки сервера длиной от 8 символов. Фильтра по имени поля нет намеренно:
// токен под «не секретным» именем утекал, а ${VAR} к этому моменту уже развёрнут
// (expandSecrets в config.go:966), то есть в значении лежит сам секрет.
// Перемаскирование безвредного значения дешевле утёкшего ключа. Значение короче
// 8 символов не маскируется — иначе случайное совпадение испортит обычный текст.
func (s *Server) Secrets() []string {
	out := make([]string, 0, len(s.Env)+len(s.Headers))
	add := func(value string) {
		value = strings.TrimSpace(value)
		if low := strings.ToLower(value); strings.HasPrefix(low, "bearer ") {
			value = strings.TrimSpace(value[len("bearer "):])
		}
		if len(value) < 8 {
			return
		}
		out = append(out, value)
	}
	for _, value := range s.Env {
		add(value)
	}
	for _, value := range s.Headers {
		add(value)
	}
	return out
}

// ScrubSecrets заменяет секреты в свободном тексте: stderr ребёнка и текст
// ошибки апстрима охотно повторяют ключ, а наружу он уходить не должен.
func ScrubSecrets(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "***")
		}
	}
	return text
}
