// echomcp is a minimal MCP stdio server that exposes a single echo tool.
//
// Usage:
//
//	echomcp
//
// The server reads MCP JSON-RPC from stdin and writes responses to stdout.
// It exposes one tool:
//
//	echo {"message": "hello"} → "hello"
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		handleLine(scanner.Bytes())
	}
}

func handleLine(line []byte) {
	var req map[string]any
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	id, ok := req["id"]
	if !ok {
		return
	}
	switch method, _ := req["method"].(string); method {
	case "initialize":
		respond(id, initResult())
	case "tools/list":
		respond(id, toolsListResult())
	case "tools/call":
		respond(id, echoResult(req))
	}
}

func initResult() map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo":      map[string]any{"name": "echomcp", "version": "0.1.0"},
		"capabilities":    map[string]any{},
	}
}

func toolsListResult() map[string]any {
	return map[string]any{
		"tools": []any{map[string]any{
			"name":        "echo",
			"description": "Echoes the message argument back to the caller.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string", "description": "Text to echo"},
				},
				"required": []string{"message"},
			},
		}},
	}
}

func echoResult(req map[string]any) map[string]any {
	params, _ := req["params"].(map[string]any)
	args, _ := params["arguments"].(map[string]any)
	msg, _ := args["message"].(string)
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": msg}},
	}
}

func respond(id, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Fprintf(os.Stdout, "%s\n", b)
}
