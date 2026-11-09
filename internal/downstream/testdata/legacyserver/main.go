// Command legacyserver стоит за stdio-сервер на mcp SDK ≤ 1.9: на
// server/discover он не отвечает и падает, как питоновский приёмный цикл,
// зато initialize и tools/list понимает.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue // уведомление
		}
		switch req.Method {
		case "server/discover":
			fmt.Fprintln(os.Stderr, "ValidationError: server/discover is not a ClientRequest")
			os.Exit(1)
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"protocolVersion": "2025-03-26",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "legacyserver", "version": "1.9.0"},
				},
			})
		case "tools/list":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"tools": []any{}},
			})
		default:
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "Method not found"},
			})
		}
	}
}
