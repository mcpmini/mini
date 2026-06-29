package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestRunList(t *testing.T) {
	t.Run("no servers configured prints message", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runList(dir, nil, &out); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(out.String(), "no servers configured") {
			t.Errorf("output = %q, want 'no servers configured'", out.String())
		}
	})

	t.Run("stdio server appears with correct transport and command", func(t *testing.T) {
		dir := t.TempDir()
		writeServer(t, dir, "gh", "name: gh\ncommand: npx\nargs: [server-github]\n")

		var out bytes.Buffer
		if err := runList(dir, nil, &out); err != nil {
			t.Fatalf("runList: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "gh") {
			t.Errorf("output missing server name 'gh': %q", got)
		}
		if !strings.Contains(got, "stdio") {
			t.Errorf("output missing transport 'stdio': %q", got)
		}
		if !strings.Contains(got, "npx") {
			t.Errorf("output missing command 'npx': %q", got)
		}
	})

	t.Run("http server shows url and transport", func(t *testing.T) {
		dir := t.TempDir()
		writeServer(t, dir, "remote", "name: remote\ntransport: http\nurl: https://example.com/mcp\n")

		var out bytes.Buffer
		if err := runList(dir, nil, &out); err != nil {
			t.Fatalf("runList: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "http") {
			t.Errorf("output missing transport 'http': %q", got)
		}
		if !strings.Contains(got, "https://example.com/mcp") {
			t.Errorf("output missing url: %q", got)
		}
	})

	t.Run("header row is always present", func(t *testing.T) {
		dir := t.TempDir()
		writeServer(t, dir, "s", "name: s\ncommand: run\n")

		var out bytes.Buffer
		runList(dir, nil, &out) //nolint:errcheck
		got := out.String()
		for _, col := range []string{"NAME", "TRANSPORT", "COMMAND / URL", "ENABLED"} {
			if !strings.Contains(got, col) {
				t.Errorf("output missing header column %q: %q", col, got)
			}
		}
	})

	t.Run("unknown server returns error", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		err := runList(dir, []string{"nope"}, &out)
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Errorf("expected error mentioning server name, got %v", err)
		}
	})
}

func TestParseArgDefs(t *testing.T) {
	t.Run("empty schema returns nil", func(t *testing.T) {
		if got := parseArgDefs(nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("required before optional, alphabetical within tier", func(t *testing.T) {
		schema := mustMarshal(t, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"beta":  map[string]any{"type": "string", "description": "second required"},
				"alpha": map[string]any{"type": "string", "description": "first required"},
				"zopt":  map[string]any{"type": "string", "description": "optional"},
			},
			"required": []string{"alpha", "beta"},
		})
		args := parseArgDefs(schema)
		if len(args) != 3 {
			t.Fatalf("got %d args, want 3", len(args))
		}
		if args[0].Name != "alpha" || !args[0].Required {
			t.Errorf("args[0] = %+v, want alpha required", args[0])
		}
		if args[1].Name != "beta" || !args[1].Required {
			t.Errorf("args[1] = %+v, want beta required", args[1])
		}
		if args[2].Name != "zopt" || args[2].Required {
			t.Errorf("args[2] = %+v, want zopt optional", args[2])
		}
	})

	t.Run("missing type defaults to any", func(t *testing.T) {
		schema := mustMarshal(t, map[string]any{
			"type":       "object",
			"properties": map[string]any{"x": map[string]any{"description": "no type field"}},
		})
		args := parseArgDefs(schema)
		if len(args) != 1 || args[0].Type != "any" {
			t.Errorf("got %+v, want type=any", args)
		}
	})
}

func TestFormatSignature(t *testing.T) {
	t.Run("no args omits parens", func(t *testing.T) {
		got := formatSignature("my_tool", nil)
		if got != "my_tool" {
			t.Errorf("got %q, want %q", got, "my_tool")
		}
	})

	t.Run("required and optional args", func(t *testing.T) {
		args := []argDef{
			{Name: "channel", Required: true},
			{Name: "text", Required: true},
			{Name: "thread_ts", Required: false},
		}
		got := formatSignature("send", args)
		want := "send(channel, text, [thread_ts])"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestPrintToolTable(t *testing.T) {
	tools := []transport.ToolDefinition{
		{
			Name:        "send_message",
			Description: "Send a message to a channel",
			InputSchema: mustMarshal(t, map[string]any{
				"type":       "object",
				"properties": map[string]any{"channel": map[string]any{"type": "string"}},
				"required":   []string{"channel"},
			}),
		},
		{
			Name:        "list_channels",
			Description: "List all channels",
		},
	}
	var out bytes.Buffer
	printToolTable(&out, nil, tools)
	got := out.String()
	if !strings.Contains(got, "TOOL") {
		t.Errorf("missing TOOL header: %q", got)
	}
	if !strings.Contains(got, "send_message(channel)") {
		t.Errorf("missing compact signature: %q", got)
	}
	if !strings.Contains(got, "list_channels") {
		t.Errorf("missing tool with no args: %q", got)
	}
	if !strings.Contains(got, "Send a message") {
		t.Errorf("missing description: %q", got)
	}
}

func TestPrintToolDetail(t *testing.T) {
	tool := transport.ToolDefinition{
		Name:        "send_message",
		Description: "Send a message to a channel",
		InputSchema: mustMarshal(t, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"channel":   map[string]any{"type": "string", "description": "Channel ID"},
				"text":      map[string]any{"type": "string", "description": "Message text"},
				"thread_ts": map[string]any{"type": "string", "description": "Thread to reply in"},
			},
			"required": []string{"channel", "text"},
		}),
	}
	var out bytes.Buffer
	printToolDetail(&out, tool)
	got := out.String()
	if !strings.Contains(got, "send_message") {
		t.Errorf("missing tool name: %q", got)
	}
	if !strings.Contains(got, "required") {
		t.Errorf("missing required label: %q", got)
	}
	if !strings.Contains(got, "optional") {
		t.Errorf("missing optional label: %q", got)
	}
	if !strings.Contains(got, "Channel ID") {
		t.Errorf("missing arg description: %q", got)
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeServer(t *testing.T, configDir, name, yaml string) {
	t.Helper()
	dir := filepath.Join(configDir, "servers")
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(yaml), 0600); err != nil {
		t.Fatalf("writeServer: %v", err)
	}
}
