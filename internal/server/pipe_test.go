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

func addSimplePipe(t *testing.T, srv interface{ AddPipes([]*pipes.CompiledPipe) }, name string, permission string) {
	t.Helper()
	pipe := config.PipeConfig{
		Name:       name,
		Permission: permission,
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "some_tool"},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	srv.AddPipes([]*pipes.CompiledPipe{cp})
}

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

func TestPipeExecution_ProtectedPipe_RequiresPermCall(t *testing.T) {
	srv := newTestServer(t)
	upstreamConn := fakeConn("some_tool")
	upstreamConn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, upstreamConn)
	addSimplePipe(t, srv, "protected_pipe", "protected")

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "user",
		"tool":   "protected_pipe",
		"params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(strings.ToLower(text), "protected") {
		t.Errorf("expected 'protected' in error for call on protected pipe, got: %s", text)
	}
}

func TestPipeExecution_ProtectedPipe_SucceedsViaPermCall(t *testing.T) {
	srv := newTestServer(t)
	upstreamConn := fakeConn("some_tool")
	upstreamConn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"done\":true}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, upstreamConn)
	addSimplePipe(t, srv, "prot_pipe", "protected")

	resp := serve(t, srv, callTool("perm_call", map[string]any{
		"server": "user",
		"tool":   "prot_pipe",
		"params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON result: %v\n%s", err, text)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Errorf("expected ok=true via perm_call, got: %v", result)
	}
}

func TestPipeExecution_InheritedProtection_BlocksViaCall(t *testing.T) {
	srv := newTestServer(t)
	upstreamConn := fakeConn("create_pr")
	upstreamConn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{
		Name: "gh",
		Permissions: &config.PermissionsConfig{Protected: []string{"create_pr"}},
	}, upstreamConn)

	pipe := config.PipeConfig{
		Name: "pr_pipe2",
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr"},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	srv.AddPipes([]*pipes.CompiledPipe{cp})

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "user",
		"tool":   "pr_pipe2",
		"params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(strings.ToLower(text), "protected") {
		t.Errorf("expected 'protected' error for inherited-protected pipe, got: %s", text)
	}
}

func TestPipeExecution_MissingPipe_ReturnsToolError(t *testing.T) {
	srv := newTestServer(t)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "user",
		"tool":   "nonexistent_pipe",
		"params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(strings.ToLower(text), "not_found") && !strings.Contains(strings.ToLower(text), "unknown") {
		t.Errorf("expected not-found error for missing pipe, got: %s", text)
	}
}

func TestPipeExecution_WithOutput(t *testing.T) {
	srv := newTestServer(t)
	upstreamConn := fakeConn("create_pr")
	upstreamConn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"number\":77}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, upstreamConn)

	pipe := config.PipeConfig{
		Name: "pr_pipe",
		Steps: []config.StepConfig{
			{ID: "create", Server: "gh", Tool: "create_pr"},
		},
		Output: map[string]string{
			"pr_number": "{{ steps.create.result.number }}",
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	srv.AddPipes([]*pipes.CompiledPipe{cp})

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "user",
		"tool":   "pr_pipe",
		"params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON: %v\n%s", err, text)
	}
	output, _ := result["output"].(map[string]any)
	if output == nil {
		t.Fatalf("expected output block in result: %v", result)
	}
	if pr, _ := output["pr_number"].(float64); int(pr) != 77 {
		t.Errorf("output.pr_number = %v, want 77", output["pr_number"])
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
