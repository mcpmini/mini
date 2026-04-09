//go:build integration

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// callSetup creates a temp config dir with a fakemcp server loaded with the given fixtures.
func callSetup(t *testing.T, fixtures map[string]string) string {
	t.Helper()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, fixtures)
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 500000\n")
	return cfg
}

func TestCLICall_BasicInvocation(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"get_item": `{"id":42,"name":"widget"}`,
	})
	stdout, _, code := runCLI(t, cfg, "call", "svc", "get_item")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	var env struct {
		Error string `json:"error"`
		Data  any    `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if env.Error != "" {
		t.Errorf("expected no error in envelope, got %q", env.Error)
	}
	if env.Data == nil {
		t.Errorf("expected data in envelope")
	}
}

func TestCLICall_WithParams(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"get_item": `{"id":1,"name":"thing"}`,
	})
	stdout, _, code := runCLI(t, cfg, "call", "svc", "get_item", `{"id":1}`)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout, `"data"`) {
		t.Errorf("expected envelope JSON with data field, got: %s", stdout)
	}
}

func TestCLICall_RawMode(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"get_item": `{"id":42,"secret":"visible"}`,
	})
	stdout, _, code := runCLI(t, cfg, "call", "-r", "svc", "get_item")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	// Raw mode: no envelope wrapper, just the upstream JSON
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("raw output not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if _, ok := raw["ok"]; ok {
		t.Errorf("raw mode should not produce an envelope, got ok field")
	}
	if raw["id"] != float64(42) {
		t.Errorf("expected id=42, got %v", raw["id"])
	}
}

func TestCLICall_MiniFormat(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"get_item": `{"id":42,"name":"widget"}`,
	})
	stdout, _, code := runCLI(t, cfg, "call", "-m", "svc", "get_item")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	// Mini format is not JSON envelope — starts with [svc.get_item]
	if !strings.Contains(stdout, "[svc.get_item]") {
		t.Errorf("expected mini header [svc.get_item], got: %s", stdout)
	}
}

func TestCLICall_WithProjection(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"get_item": `{"id":1,"secret":"hidden","name":"Alice"}`,
	})
	writeProjection(t, cfg, "svc", "get_item:\n  exclude_always: [secret]\n")

	stdout, _, code := runCLI(t, cfg, "call", "svc", "get_item")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if strings.Contains(stdout, "hidden") {
		t.Errorf("projected field 'secret' should be excluded, got: %s", stdout)
	}
	var env struct {
		Elided []string `json:"elided"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	found := false
	for _, e := range env.Elided {
		if e == "secret" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'secret' in elided list, got %v", env.Elided)
	}
}

func TestCLICall_ToolError_ExitsOne(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"fail_tool": `{"__mcp_error":"something went wrong"}`,
	})
	_, stderr, code := runCLI(t, cfg, "call", "svc", "fail_tool")
	if code != 1 {
		t.Errorf("expected exit 1 on tool error, got %d", code)
	}
	if !strings.Contains(stderr, "something went wrong") {
		t.Errorf("expected error message in stderr, got: %s", stderr)
	}
}

func TestCLICall_ServerNotFound_ExitsTwo(t *testing.T) {
	cfg := t.TempDir()
	writeConfig(t, cfg, "")
	_, stderr, code := runCLI(t, cfg, "call", "nosuchserver", "some_tool")
	if code != 2 {
		t.Errorf("expected exit 2 for unknown server, got %d", code)
	}
	if !strings.Contains(stderr, "nosuchserver") {
		t.Errorf("expected server name in stderr, got: %s", stderr)
	}
}

func TestCLICall_MissingArgs_ExitsTwo(t *testing.T) {
	cfg := t.TempDir()
	writeConfig(t, cfg, "")
	_, _, code := runCLI(t, cfg, "call", "svc")
	if code != 2 {
		t.Errorf("expected exit 2 for missing tool arg, got %d", code)
	}
}

func TestCLICall_InvalidJSON_ExitsTwo(t *testing.T) {
	cfg := callSetup(t, map[string]string{"get_item": `{}`})
	_, stderr, code := runCLI(t, cfg, "call", "svc", "get_item", "not-json")
	if code != 2 {
		t.Errorf("expected exit 2 for invalid JSON params, got %d", code)
	}
	if !strings.Contains(stderr, "JSON") {
		t.Errorf("expected JSON error message, got: %s", stderr)
	}
}

func TestCLICall_OutputIsValidJSON(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"list_items": `[{"id":1},{"id":2},{"id":3}]`,
	})
	stdout, _, code := runCLI(t, cfg, "call", "svc", "list_items")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	var v any
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Errorf("stdout must be valid JSON for piping to jq: %v\nstdout: %s", err, stdout)
	}
}

func TestCLIPermCall_BasicInvocation(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"create_item": `{"id":99,"created":true}`,
	})
	stdout, _, code := runCLI(t, cfg, "perm-call", "svc", "create_item", `{"name":"new"}`)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if env.Error != "" {
		t.Errorf("expected no error in envelope, got %q", env.Error)
	}
}

func TestCLIPermCall_RawMode(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"delete_item": `{"deleted":true}`,
	})
	stdout, _, code := runCLI(t, cfg, "perm-call", "-r", "svc", "delete_item", `{"id":1}`)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("raw output not valid JSON: %v", err)
	}
	if raw["deleted"] != true {
		t.Errorf("expected deleted=true, got %v", raw["deleted"])
	}
}

func TestCLICall_ConfigFormatMini(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"get_item": `{"id":1,"name":"thing"}`,
	})
	// Set response_format: mini in config — call should default to mini without -m
	writeConfig(t, cfg, "inline_threshold: 500000\nresponse_format: mini\n")
	stdout, _, code := runCLI(t, cfg, "call", "svc", "get_item")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "[svc.get_item]") {
		t.Errorf("expected mini format from config default, got: %s", stdout)
	}
}

func TestCLICall_FlagOverridesConfigFormat(t *testing.T) {
	cfg := callSetup(t, map[string]string{
		"get_item": `{"id":1}`,
	})
	writeConfig(t, cfg, "inline_threshold: 500000\nresponse_format: mini\n")
	// -j should override mini default
	stdout, _, code := runCLI(t, cfg, "call", "-j", "svc", "get_item")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	var env struct{ Error string `json:"error"` }
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Errorf("-j flag should produce JSON envelope: %v\nstdout: %s", err, stdout)
	}
}

func TestCLICall_StdinParams(t *testing.T) {
	cfg := callSetup(t, map[string]string{"get_item": `{"id":42,"name":"widget"}`})
	stdout, _, code := runCLIWithStdin(t, `{"id":42}`, cfg, "call", "svc", "get_item", "-")
	if code != 0 {
		t.Fatalf("call with stdin params should exit 0, got %d", code)
	}
	var env struct {
		Error string `json:"error"`
		Data  any    `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if env.Error != "" {
		t.Errorf("expected no error, got %q", env.Error)
	}
	if env.Data == nil {
		t.Error("expected data in envelope")
	}
}

func TestCLICall_ProjectionWritesFile(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{
		"get_item": `{"id":1,"secret":"hidden","name":"Alice"}`,
	})
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 500000\nresponse_dir: "+respDir+"\n")
	writeProjection(t, cfg, "svc", "get_item:\n  exclude_always: [secret]\n")

	stdout, _, code := runCLI(t, cfg, "call", "svc", "get_item")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", code, stdout)
	}
	var env struct {
		File  *string `json:"file"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse envelope: %v\nstdout: %s", err, stdout)
	}
	if env.Error != "" {
		t.Fatalf("unexpected error: %s", env.Error)
	}
	if env.File == nil {
		t.Error("expected 'file' field in envelope when projection elided fields, got nil")
	}
}

func TestCLICall_UnreachableServer_ExitsNonZero(t *testing.T) {
	cfg := t.TempDir()
	// HTTP transport is lazy — dial succeeds, error surfaces on first call (exit 1).
	writeHTTPServerYAML(t, cfg, "dead", "http://127.0.0.1:19998")
	_, stderr, code := runCLI(t, cfg, "call", "dead", "some_tool")
	if code == 0 {
		t.Errorf("unreachable server should exit non-zero, got 0\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "connect") && !strings.Contains(stderr, "refused") && !strings.Contains(stderr, "dead") {
		t.Errorf("expected connection error in stderr, got: %s", stderr)
	}
}
