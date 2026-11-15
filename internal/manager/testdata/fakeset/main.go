// Command fakeset stands in for the proxy in manager tests: it reads the port
// from a set config, answers every request, and speaks the reload contract —
// SIGHUP rereads servers.json and writes reload.status next to it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

// probeName — переменная, которую тест выдаёт серверу через envAllow: процесс
// набора видит её только после перезапуска с новым окружением.
const probeName = "RELOAD_PROBE"

type setConfig struct {
	Transport struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"transport"`
	Servers []struct {
		Name string `json:"name"`
	} `json:"servers"`
}

func readConfig(path string) (setConfig, error) {
	var cfg setConfig
	body, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(body, &cfg)
}

func serverNames(cfg setConfig) []string {
	out := make([]string, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		out = append(out, s.Name)
	}
	return out
}

var (
	mu    sync.Mutex
	gen   int
	names []string
)

// startChild поднимает дочерний процесс без обработчиков сигналов — как
// настоящий stdio-сервер набора, — когда тест положил рядом метку child.want.
// Под SIGHUP всей группе такой ребёнок умирает; pid для проверки — в child.pid.
func startChild(dir string) {
	if _, err := os.Stat(filepath.Join(dir, "child.want")); err != nil {
		return
	}
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		log.Fatalf("fakeset: дочерний процесс: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.pid"),
		[]byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		log.Fatalf("fakeset: %v", err)
	}
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := fs.String("config", "", "путь к servers.json")
	_ = fs.Parse(args)
	if *path == "" {
		log.Fatal("fakeset: нет --config")
	}
	cfg, err := readConfig(*path)
	if err != nil {
		log.Fatalf("fakeset: %v", err)
	}
	names = serverNames(cfg)
	// Менеджерские тесты проверяют, какой каталог состояния достался набору.
	if err := os.WriteFile(filepath.Join(filepath.Dir(*path), "child-env"),
		[]byte(os.Getenv("MCPRIG_DATA_DIR")), 0o600); err != nil {
		log.Fatalf("fakeset: %v", err)
	}
	startChild(filepath.Dir(*path))

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGHUP)
	go func() {
		for range sigs {
			reload(*path)
		}
	}()

	host := cfg.Transport.Host
	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, cfg.Transport.Port)
	log.Printf("fakeset слушает %s", addr)
	if err := http.ListenAndServe(addr, http.HandlerFunc(handle)); err != nil {
		log.Fatalf("fakeset: %v", err)
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/applied" {
		mu.Lock()
		out := map[string]any{"generation": gen, "servers": names}
		mu.Unlock()
		// Окружение процесса набора: видно только то, что супервизор передал
		// при старте, — так тест ловит правку envAllow, не доехавшую до набора.
		out["probe"] = os.Getenv(probeName)
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	fmt.Fprintln(w, "ok")
}

// reload — контракт процесса набора: поколение растёт на каждой попытке, битый
// конфиг не применяется и попадает в error, ответ пишется атомарно.
func reload(path string) {
	dir := filepath.Dir(path)
	// Метка для теста «процесс умер до ответа»: набор выходит, как при падении.
	if _, err := os.Stat(filepath.Join(dir, "reload.die")); err == nil {
		os.Exit(1)
	}
	mu.Lock()
	gen++
	out := map[string]any{
		"generation":    gen,
		"applied":       []string{},
		"restartNeeded": []string{},
	}
	if nonce := readNonce(dir); nonce != "" {
		out["nonce"] = nonce
	}
	if cfg, err := readConfig(path); err != nil {
		out["error"] = err.Error()
	} else {
		names = serverNames(cfg)
		out["applied"] = names
	}
	mu.Unlock()
	writeStatus(filepath.Join(dir, "reload.status"), out)
}

// readNonce повторяет метку попытки из reload.request, как настоящий процесс.
func readNonce(dir string) string {
	body, err := os.ReadFile(filepath.Join(dir, "reload.request"))
	if err != nil {
		return ""
	}
	var req struct {
		Nonce string `json:"nonce"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	return req.Nonce
}

func writeStatus(path string, out map[string]any) {
	body, err := json.Marshal(out)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
