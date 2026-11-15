package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/mcpserver"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func writeServers(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readStatus(t *testing.T, path string) reloadStatus {
	t.Helper()
	st, err := readStatusSoft(path)
	if err != nil {
		t.Fatalf("reload.status: %v", err)
	}
	return st
}

// readStatusSoft не падает, пока файла нет: супервизор читает так же.
func readStatusSoft(path string) (reloadStatus, error) {
	var st reloadStatus
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, err
	}
	return st, nil
}

// Одна попытка перечитки: контракт reload.status и fail-closed на битом файле.
func TestReloadOnceWritesStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	statusPath := filepath.Join(dir, "reload.status")
	one := `{"servers": [{"name": "alpha", "command": ["downstream"], "enabled": true}]}`
	writeServers(t, path, one)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	mgr := mcpserver.New(cfg, "test", nil)
	defer mgr.Close()

	if res := reloadOnce(mgr, nil, path, quietLog()); res.Generation != 1 || len(res.Applied) != 0 {
		t.Fatalf("первая попытка без правок: %+v", res)
	}
	st := readStatus(t, statusPath)
	if st.Generation != 1 || st.Error != "" || st.Applied == nil || st.RestartNeeded == nil {
		t.Fatalf("status после первой попытки: %+v", st)
	}
	if _, err := time.Parse(time.RFC3339, st.At); err != nil {
		t.Fatalf("at = %q: %v", st.At, err)
	}
	if info, err := os.Stat(statusPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("режим reload.status: %v %v", info, err)
	}

	writeServers(t, path, `{"servers": [
		{"name": "alpha", "command": ["downstream"], "enabled": true},
		{"name": "beta", "command": ["downstream"], "enabled": true}
	]}`)
	res := reloadOnce(mgr, nil, path, quietLog())
	if res.Generation != 2 || len(res.Applied) != 1 || res.Applied[0] != "added:beta" {
		t.Fatalf("добавление beta: %+v", res)
	}
	if st := readStatus(t, statusPath); st.Generation != 2 || len(st.Applied) != 1 {
		t.Fatalf("status после добавления: %+v", st)
	}

	writeServers(t, path, `{"servers": [`)
	res = reloadOnce(mgr, nil, path, quietLog())
	if res.Generation != 3 || res.Err == "" || len(res.Applied) != 0 {
		t.Fatalf("битый файл: %+v", res)
	}
	if st := readStatus(t, statusPath); st.Generation != 3 || st.Error == "" {
		t.Fatalf("status после битого файла: %+v", st)
	}
	// Прежний конфиг остался в силе: возврат к тому же файлу ничего не меняет.
	writeServers(t, path, `{"servers": [
		{"name": "alpha", "command": ["downstream"], "enabled": true},
		{"name": "beta", "command": ["downstream"], "enabled": true}
	]}`)
	if res := reloadOnce(mgr, nil, path, quietLog()); len(res.Applied) != 0 || res.Generation != 4 {
		t.Fatalf("после битого файла живой конфиг не сохранился: %+v", res)
	}
}

// SIGHUP перечитывает конфиг на живом процессе и оставляет ack в reload.status.
func TestServeReloadsOnSIGHUP(t *testing.T) {
	if testing.Short() {
		t.Skip("поднимает бинарь и процесс набора")
	}
	bin := filepath.Join(t.TempDir(), "mcprig")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка: %v\n%s", err, out)
	}

	// Свободный порт: слушатель набора должен подняться без чужих помех.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	statusPath := filepath.Join(dir, "reload.status")
	body := `{"servers": [], "transport": {"type": "http", "port": ` + fmt.Sprint(port) + `, "allowAnonymous": true}}`
	writeServers(t, path, body)

	cmd := exec.Command(bin, "serve", "-config", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("запуск: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_, _ = cmd.Process.Wait()
	})
	// Ждём слушатель: SIGHUP до установки обработчика убил бы процесс.
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	deadline := time.Now().Add(20 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("слушатель не поднялся: %s", stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	writeServers(t, path, `{"servers": [{"name": "alpha", "command": ["downstream"], "enabled": true}],
		"transport": {"type": "http", "port": `+fmt.Sprint(port)+`, "allowAnonymous": true}}`)
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("SIGHUP: %v", err)
	}
	for {
		if time.Now().After(deadline) {
			t.Fatalf("SIGHUP не применён, последний status: %+v (stderr: %s)", readStatus(t, statusPath), stderr.String())
		}
		if st, err := readStatusSoft(statusPath); err == nil && st.Generation >= 1 {
			if len(st.Applied) != 1 || st.Applied[0] != "added:alpha" {
				t.Fatalf("applied = %v", st.Applied)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
