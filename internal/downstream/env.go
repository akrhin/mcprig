package downstream

import (
	"os"
	"sort"
	"strings"

	"github.com/sintez/mcprig/internal/config"
)

// baseEnvKeep — что stdio-сервер получает по умолчанию. Остальное окружение
// владельца (ключи, токены, прокси-креды) вниз не уходит: пакет, который
// выгребает os.Environ(), не должен видеть чужие секреты.
var baseEnvKeep = map[string]bool{
	"PATH": true, "HOME": true, "LANG": true, "TZ": true, "TMPDIR": true,
	"NODE_EXTRA_CA_CERTS": true,
}

var baseEnvPrefixes = []string{"LC_", "XDG_", "SSL_"}

// BaseEnv — whitelist окружения stdio-процесса.
func BaseEnv() []string { return envSelect(nil) }

// EnvFor — BaseEnv плюс переменные из srv.EnvAllow: сервер получает из
// окружения владельца только то, что перечислено явно.
func EnvFor(srv *config.Server) []string {
	if srv == nil {
		return BaseEnv()
	}
	return envSelect(srv.EnvAllow)
}

func envSelect(extra []string) []string {
	want := make(map[string]bool, len(extra))
	for _, name := range extra {
		want[name] = true
	}
	out := make([]string, 0, 32)
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if baseEnvKeep[name] || want[name] || hasEnvPrefix(name) {
			out = append(out, kv)
		}
	}
	sort.Strings(out)
	return out
}

func hasEnvPrefix(name string) bool {
	for _, prefix := range baseEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
