// Command fakeset stands in for the proxy in manager tests: it reads the port
// from a set config and answers every request, so the supervisor's readiness
// probe sees a live listener.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

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
	body, err := os.ReadFile(*path)
	if err != nil {
		log.Fatalf("fakeset: %v", err)
	}
	var cfg struct {
		Transport struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		} `json:"transport"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		log.Fatalf("fakeset: %v", err)
	}
	host := cfg.Transport.Host
	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, cfg.Transport.Port)
	log.Printf("fakeset слушает %s", addr)
	if err := http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})); err != nil {
		log.Fatalf("fakeset: %v", err)
	}
}
