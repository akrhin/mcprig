// testmcp is a tiny stdio MCP server used as a fixture by lifecycle tests:
// it starts fast, answers tools/list and has one tool that fails on purpose.
package main

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text" jsonschema:"text to echo back"`
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "testmcp", Version: "0.0.1"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo the given text back."},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "fail", Description: "Always returns an error."},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return nil, nil, errors.New("fixture failure")
		})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Println("testmcp:", err)
		os.Exit(1)
	}
}
