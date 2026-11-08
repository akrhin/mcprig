// mcp-manager: one binary that runs MCP sets (proxies), the control panel and
// the dashboard.
package main

import (
	"fmt"
	"os"
)

var version = "0.0.0-dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	fmt.Fprintln(os.Stderr, "mcp-manager: not implemented yet, see docs/CURRENT-GOAL.md")
	os.Exit(1)
}
