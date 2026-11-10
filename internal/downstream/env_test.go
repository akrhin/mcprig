package downstream

import (
	"os"
	"strings"
	"testing"

	"github.com/akrhin/mcprig/internal/config"
)

// Окружение — граница доверия: ключ хоста вниз не уходит, пока его не
// перечислили в envAllow.
func TestEnvWhitelist(t *testing.T) {
	t.Setenv("MCPRIG_TEST_SECRET", "s3cr3t")
	t.Setenv("LC_ALL", "C")
	t.Setenv("XDG_DATA_HOME", "/tmp/x")

	got := strings.Join(EnvFor(&config.Server{}), "\n")
	if strings.Contains(got, "MCPRIG_TEST_SECRET") {
		t.Fatalf("секрет хоста утёк в сервер: %s", got)
	}
	for _, want := range []string{"PATH=", "LC_ALL=C", "XDG_DATA_HOME=/tmp/x"} {
		if !strings.Contains(got, want) {
			t.Errorf("whitelist потерял %q: %s", want, got)
		}
	}

	// envAllow — единственный способ провести переменную владельца вниз.
	got = strings.Join(EnvFor(&config.Server{EnvAllow: []string{"MCPRIG_TEST_SECRET"}}), "\n")
	if !strings.Contains(got, "MCPRIG_TEST_SECRET=s3cr3t") {
		t.Fatalf("envAllow не сработал: %s", got)
	}
	if strings.Contains(strings.Join(BaseEnv(), "\n"), "MCPRIG_TEST_SECRET") {
		t.Error("BaseEnv не должен знать про envAllow")
	}
	if _, ok := os.LookupEnv("MCPRIG_TEST_SECRET"); !ok {
		t.Fatal("t.Setenv не сработал")
	}
}
