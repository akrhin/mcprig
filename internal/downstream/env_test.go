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
	t.Setenv("OWN_SECRET", "own")
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
	got = strings.Join(EnvFor(&config.Server{EnvAllow: []string{"OWN_SECRET"}}), "\n")
	if !strings.Contains(got, "OWN_SECRET=own") {
		t.Fatalf("envAllow не сработал: %s", got)
	}
	if strings.Contains(strings.Join(BaseEnv(), "\n"), "OWN_SECRET") {
		t.Error("BaseEnv не должен знать про envAllow")
	}
	if _, ok := os.LookupEnv("MCPRIG_TEST_SECRET"); !ok {
		t.Fatal("t.Setenv не сработал")
	}
}

// Резервные имена не доходят до exec.Cmd.Env, даже когда перечислены в
// envAllow или заданы в env сервера.
func TestReservedEnvNeverPassesDown(t *testing.T) {
	reserved := []string{"MCPRIG_LEAKTEST", "BAO_TOKEN", "OPENBAO_TOKEN"}
	for _, name := range reserved {
		t.Setenv(name, "secret")
	}
	srv := &config.Server{EnvAllow: reserved}
	cmdEnv := strings.Join(append(withoutReserved(EnvFor(srv)),
		flatten(map[string]string{"OPENBAO_TOKEN": "x", "OWN": "1"})...), "\n")
	for _, name := range reserved {
		if strings.Contains(cmdEnv, name+"=") {
			t.Errorf("%s ушёл вниз: %s", name, cmdEnv)
		}
	}
	if !strings.Contains(cmdEnv, "OWN=1") {
		t.Errorf("свой env сервера потерян: %s", cmdEnv)
	}
}
