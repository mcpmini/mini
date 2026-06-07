//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/pipes"
)

func TestPipeExecution_ViaCallTool(t *testing.T) {
	srv := newTestServer(t)

	upstreamConn := fakeConn("create_pr", "post_message")
	upstreamConn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"number\":42}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, upstreamConn)

	pipe := config.PipeConfig{
		Name:        "create_and_notify",
		Description: "Creates PR and notifies",
		Steps: []config.StepConfig{
			{ID: "create", Server: "gh", Tool: "create_pr"},
			{ID: "notify", Server: "gh", Tool: "post_message", ContinueOnError: true},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	srv.AddPipes([]*pipes.CompiledPipe{cp})

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "user",
		"tool":   "create_and_notify",
		"params": map[string]any{},
	}))

	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON response, got: %s\nerr: %v", text, err)
	}
	ok, _ := result["ok"].(bool)
	if !ok {
		t.Errorf("expected ok=true in pipe result, got: %v", result)
	}
	if result["server"] != "user" {
		t.Errorf("server = %q, want %q", result["server"], "user")
	}
	if result["tool"] != "create_and_notify" {
		t.Errorf("tool = %q, want %q", result["tool"], "create_and_notify")
	}
}

func TestPipeExecution_AppearsInList(t *testing.T) {
	srv := newTestServer(t)

	pipe := config.PipeConfig{
		Name:        "list_me",
		Description: "A pipe in the list",
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "some_tool"},
		},
	}
	cp, _ := pipes.Compile(pipe)
	srv.AddPipes([]*pipes.CompiledPipe{cp})

	resp := serve(t, srv, callTool("list", nil))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "user.list_me") {
		t.Errorf("expected pipe 'user.list_me' in list output: %s", text)
	}
}

func TestPipeExecution_ReservedServerNameRejected(t *testing.T) {
	srv := newTestServer(t)

	err := srv.AddUpstream(context.Background(), config.ServerConfig{
		Name:    "user",
		Command: "echo",
	})
	if err == nil {
		t.Error("expected error when adding server with reserved name 'user'")
	}
}

func TestPipeExecution_RuntimeAddServerRejectsReserved(t *testing.T) {
	srv := newTestServer(t)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{
			"name":    "user",
			"command": "echo",
		},
	}))

	text := toolResultText(t, resp)
	if !strings.Contains(strings.ToLower(text), "reserved") {
		t.Errorf("expected 'reserved' in error response, got: %s", text)
	}
}
