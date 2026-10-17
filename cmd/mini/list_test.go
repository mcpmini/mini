package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/transport"
)

func TestRunList(t *testing.T) {
	t.Run("no servers configured points to init", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		if err := runList(dir, nil, &out); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(out.String(), "no servers configured") || !strings.Contains(out.String(), "mini init") {
			t.Errorf("output = %q, want the empty state and a pointer to mini init", out.String())
		}
	})

	t.Run("stdio server appears with correct transport and command", func(t *testing.T) {
		dir := t.TempDir()
		configtest.WriteServer(t, dir, config.ServerConfig{Name: "gh", Command: "npx", Args: []string{"server-github"}})

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
		configtest.WriteServer(
			t,
			dir,
			config.ServerConfig{Name: "remote", Transport: "http", URL: "https://example.com/mcp"},
		)

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
		configtest.WriteServer(t, dir, config.ServerConfig{Name: "s", Command: "run"})

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

func TestPrintToolTable(t *testing.T) {
	tools := []transport.ToolDefinition{
		{
			Name: "notion-search", Title: json.RawMessage(`"Search Notion"`),
			Description: strings.Repeat("Long description. ", 100),
			InputSchema: json.RawMessage(`{"properties":{"query":{"type":"string"}}}`),
		},
		{Name: "notion-fetch", Description: "Full description stays in the detail view"},
	}
	var out bytes.Buffer
	if err := printToolTable(&out, tools); err != nil {
		t.Fatalf("printToolTable: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header and one row per tool, got %q", out.String())
	}
	for i, want := range []string{"TOOL TITLE", "notion-search Search Notion", "notion-fetch"} {
		if got := strings.Join(strings.Fields(lines[i]), " "); got != want {
			t.Errorf("row %d = %q, want %q", i, got, want)
		}
	}
}

func TestToolListTitle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		raw         string
		annotations string
		want        string
	}{
		{name: "missing"},
		{name: "null", raw: "null"},
		{name: "invalid type", raw: `{}`},
		{name: "annotation title", annotations: `{"title":"Search pages"}`, want: "Search pages"},
		{name: "title takes precedence", raw: `"Search Notion"`, annotations: `{"title":"Search pages"}`, want: "Search Notion"},
		{name: "invalid annotations", annotations: `{"title":{}}`},
		{name: "whitespace", raw: `" Search\tNotion\n pages\r "`, want: "Search Notion pages"},
		{name: "long unicode", raw: `"` + strings.Repeat("é", 80) + `"`, want: strings.Repeat("é", 59) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := toolListTitle(
				transport.ToolDefinition{Title: json.RawMessage(tc.raw), Annotations: json.RawMessage(tc.annotations)},
			)
			if got != tc.want {
				t.Errorf("title = %q, want %q", got, tc.want)
			}
		})
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
	if err := printToolDetail(&out, tool); err != nil {
		t.Fatalf("printToolDetail: %v", err)
	}
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
