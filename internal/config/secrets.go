package config

import (
	"regexp"
	"strings"
)

// secretKey опознаёт поля конфигурации, в которых лежит секрет: ключ, токен,
// пароль, заголовок авторизации.
var secretKey = regexp.MustCompile(`(?i)(key|token|secret|pass|auth|credential)`)

// Secrets собирает значения, которые нельзя выпускать наружу: env и заголовки
// сервера с «секретным» именем. Значение короче 8 символов не маскируется —
// иначе случайное совпадение испортит обычный текст.
func (s *Server) Secrets() []string {
	out := make([]string, 0, len(s.Env)+len(s.Headers))
	add := func(name, value string) {
		if !secretKey.MatchString(name) {
			return
		}
		value = strings.TrimSpace(value)
		if low := strings.ToLower(value); strings.HasPrefix(low, "bearer ") {
			value = strings.TrimSpace(value[len("bearer "):])
		}
		if len(value) < 8 {
			return
		}
		out = append(out, value)
	}
	for name, value := range s.Env {
		add(name, value)
	}
	for name, value := range s.Headers {
		add(name, value)
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
