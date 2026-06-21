//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pipeResult mirrors pipes.Result for integration test assertions.
type pipeResult struct {
	Server     string           `json:"server"`
	Tool       string           `json:"tool"`
	OK         bool             `json:"ok"`
	Output     map[string]any   `json:"output"`
	Steps      []pipeStepResult `json:"steps"`
	Error      string           `json:"error"`
	FailedStep string           `json:"failed_step"`
	LatencyMs  int64            `json:"latency_ms"`
}

type pipeStepResult struct {
	ID              string `json:"id"`
	OK              bool   `json:"ok"`
	Skipped         bool   `json:"skipped"`
	ContinueOnError bool   `json:"continue_on_error"`
	Error           string `json:"error"`
}

// execPipeCall calls a pipe on the user server. It uses the "params" key (not
// "args") because executeParams reads pipe inputs from the "params" JSON field.
func (c *mcpClient) execPipeCall(name string, inputs map[string]any) string {
	c.t.Helper()
	if inputs == nil {
		inputs = map[string]any{}
	}
	raw := c.mustCall("tools/call", map[string]any{
		"name": "call",
		"arguments": map[string]any{
			"server": "user",
			"tool":   name,
			"params": inputs,
		},
	})
	return toolCallText(c.t, raw)
}

func writePipe(t *testing.T, configDir, name, content string) {
	t.Helper()
	dir := filepath.Join(configDir, "pipes")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func writePipesConfig(t *testing.T, configDir string) {
	t.Helper()
	writeConfig(t, configDir, "inline_threshold: 50000\nenable_pipes: true\n")
}

func parsePipeResult(t *testing.T, text string) pipeResult {
	t.Helper()
	var pr pipeResult
	if err := json.Unmarshal([]byte(text), &pr); err != nil {
		t.Fatalf("parse pipe result: %v\ntext: %s", err, text)
	}
	return pr
}

func findStep(steps []pipeStepResult, id string) (pipeStepResult, bool) {
	for _, s := range steps {
		if s.ID == id {
			return s, true
		}
	}
	return pipeStepResult{}, false
}

// twoServerConfig sets up a config dir with two static fake servers (no fault control).
func twoServerConfig(t *testing.T, ghFixtures, slFixtures map[string]string) string {
	t.Helper()
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "github", mockFixtureDir(t, ghFixtures))
	writeFakeServer(t, cfg, "slack", mockFixtureDir(t, slFixtures))
	return cfg
}

// Standard two-step pipe: create PR then notify Slack, with output block.
const createAndNotifyPipe = `name: create_and_notify
description: Create PR and notify Slack
inputs:
  title:
    type: string
    required: true
steps:
  - id: pr
    server: github
    tool: create_pull_request
    args:
      title: "{{ inputs.title }}"
      base: main
  - id: notify
    server: slack
    tool: post_message
    silent: true
    continue_on_error: true
    args:
      channel: "#eng"
      text: "PR ready: {{ steps.pr.result.html_url }}"
output:
  pr_url: "{{ steps.pr.result.html_url }}"
  pr_number: "{{ steps.pr.result.number }}"
`

func TestPipes_HappyPath(t *testing.T) {
	cfg := twoServerConfig(t,
		map[string]string{"create_pull_request": `{"__write_op":true}`},
		map[string]string{"post_message": `{"__write_op":true}`},
	)
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)
	client := startServer(t, cfg)

	text := client.execPipeCall("create_and_notify", map[string]any{"title": "fix: auth"})
	pr := parsePipeResult(t, text)

	if !pr.OK {
		t.Fatalf("expected ok=true, got ok=false: %s", pr.Error)
	}
	if pr.Output["pr_url"] == nil || pr.Output["pr_url"] == "" {
		t.Errorf("expected pr_url in output, got: %v", pr.Output)
	}
	if pr.Output["pr_number"] == nil {
		t.Errorf("expected pr_number in output, got: %v", pr.Output)
	}

	if prStep, ok := findStep(pr.Steps, "pr"); !ok || !prStep.OK {
		t.Error("expected pr step to succeed")
	}
	if notifyStep, ok := findStep(pr.Steps, "notify"); !ok || !notifyStep.OK {
		t.Error("expected notify step to succeed")
	}
}

func TestPipes_OutputInterpolatesUpstreamFields(t *testing.T) {
	cfg := twoServerConfig(t,
		map[string]string{"create_pull_request": `{"__write_op":true}`},
		map[string]string{"post_message": `{"__write_op":true}`},
	)
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)
	client := startServer(t, cfg)

	text := client.execPipeCall("create_and_notify", map[string]any{"title": "fix: auth"})
	pr := parsePipeResult(t, text)

	// create_pull_request write_op returns html_url like https://github.com/.../pull/101
	url, _ := pr.Output["pr_url"].(string)
	if !strings.Contains(url, "github.com") {
		t.Errorf("expected github URL in pr_url, got: %q", url)
	}
}

func TestPipes_ContinueOnError_SlackFails_PipeSucceeds(t *testing.T) {
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "github", mockFixtureDir(t, map[string]string{"create_pull_request": `{"__write_op":true}`}))
	slDir := mockFixtureDir(t, map[string]string{"post_message": `{"__write_op":true}`})
	const faultJSON = `{"tool":"post_message","type":"error_response","message":"channel_not_found"}`
	writeFaultServer(t, faultServerParams{ConfigDir: cfg, ServerName: "slack", Fixtures: slDir, FaultJSON: faultJSON})
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)
	client := startServer(t, cfg)

	text := client.execPipeCall("create_and_notify", map[string]any{"title": "fix: auth"})
	pr := parsePipeResult(t, text)

	if !pr.OK {
		t.Fatalf("expected pipe to succeed despite Slack failure, got: %s", pr.Error)
	}
	prStep, _ := findStep(pr.Steps, "pr")
	if !prStep.OK {
		t.Error("expected pr step to succeed")
	}
	notifyStep, ok := findStep(pr.Steps, "notify")
	if !ok {
		t.Fatal("expected notify step in steps list")
	}
	if notifyStep.OK {
		t.Error("expected notify step to fail")
	}
	if notifyStep.Error == "" {
		t.Error("expected error message on failed notify step")
	}
	if pr.Output["pr_url"] == nil {
		t.Error("expected output to still be populated after Slack failure")
	}
}

func TestPipes_StepFailure_AbortsRemainingSteps(t *testing.T) {
	const abortPipe = `name: abort_pipe
description: Aborts on first failure
steps:
  - id: first
    server: github
    tool: failing_tool
    args:
      x: "1"
  - id: second
    server: slack
    tool: post_message
    args:
      channel: "#eng"
      text: done
`
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "github", mockFixtureDir(t, map[string]string{
		"failing_tool": `{"__mcp_error":"simulated upstream failure"}`,
	}))
	writeFakeServer(t, cfg, "slack", mockFixtureDir(t, map[string]string{"post_message": `{"__write_op":true}`}))
	writePipe(t, cfg, "abort_pipe", abortPipe)
	client := startServer(t, cfg)

	text := client.execTool("user", "abort_pipe", nil)
	pr := parsePipeResult(t, text)

	if pr.OK {
		t.Fatal("expected ok=false when step fails without continue_on_error")
	}
	if pr.FailedStep != "first" {
		t.Errorf("expected failed_step=first, got %q", pr.FailedStep)
	}
	if _, ok := findStep(pr.Steps, "second"); ok {
		t.Error("expected second step to not execute after abort")
	}
}

func TestPipes_SkippedStep(t *testing.T) {
	const conditionalPipe = `name: conditional_pipe
description: Pipe with conditional step
steps:
  - id: create
    server: github
    tool: create_pull_request
    args:
      title: test
      base: main
  - id: label
    if: "{{ false }}"
    server: github
    tool: add_labels
    args:
      number: "{{ steps.create.result.number }}"
      labels: [go]
output:
  pr_number: "{{ steps.create.result.number }}"
`
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "github", mockFixtureDir(t, map[string]string{
		"create_pull_request": `{"__write_op":true}`,
		"add_labels":          `{"__write_op":true}`,
	}))
	writePipe(t, cfg, "conditional_pipe", conditionalPipe)
	client := startServer(t, cfg)

	text := client.execTool("user", "conditional_pipe", nil)
	pr := parsePipeResult(t, text)

	if !pr.OK {
		t.Fatalf("expected ok=true, got: %s", pr.Error)
	}
	labelStep, ok := findStep(pr.Steps, "label")
	if !ok {
		t.Fatal("expected label step to appear in steps")
	}
	if !labelStep.Skipped {
		t.Error("expected label step to be marked skipped")
	}
	if pr.Output["pr_number"] == nil {
		t.Error("expected output from non-skipped create step")
	}
}

func TestPipes_SetStep_ComputesValues(t *testing.T) {
	const setPipe = `name: classify_pipe
description: Uses a set step to classify PR
steps:
  - id: create
    server: github
    tool: create_pull_request
    args:
      title: my PR
      base: main
  - id: classify
    set:
      is_high_number: "{{ steps.create.result.number > 0 }}"
output:
  is_high_number: "{{ steps.classify.is_high_number }}"
`
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "github", mockFixtureDir(t, map[string]string{"create_pull_request": `{"__write_op":true}`}))
	writePipe(t, cfg, "classify_pipe", setPipe)
	client := startServer(t, cfg)

	text := client.execTool("user", "classify_pipe", nil)
	pr := parsePipeResult(t, text)

	if !pr.OK {
		t.Fatalf("expected ok=true, got: %s", pr.Error)
	}
	classifyStep, ok := findStep(pr.Steps, "classify")
	if !ok || !classifyStep.OK {
		t.Error("expected classify step to succeed")
	}
	if pr.Output["is_high_number"] == nil {
		t.Errorf("expected is_high_number in output, got: %v", pr.Output)
	}
}

func TestPipes_MissingRequiredInput_FailsBeforeAnyStep(t *testing.T) {
	cfg := twoServerConfig(t,
		map[string]string{"create_pull_request": `{"__write_op":true}`},
		map[string]string{"post_message": `{"__write_op":true}`},
	)
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)
	client := startServer(t, cfg)

	// Call without required "title" input.
	text := client.execTool("user", "create_and_notify", nil)
	pr := parsePipeResult(t, text)

	if pr.OK {
		t.Fatal("expected ok=false when required input is missing")
	}
	if !strings.Contains(pr.Error, "title") {
		t.Errorf("expected error to mention missing field 'title', got: %s", pr.Error)
	}
	if len(pr.Steps) != 0 {
		t.Errorf("expected no steps executed when input validation fails, got %d steps", len(pr.Steps))
	}
}

func TestPipes_AppearsInList(t *testing.T) {
	cfg := twoServerConfig(t,
		map[string]string{"create_pull_request": `{"__write_op":true}`},
		map[string]string{"post_message": `{"__write_op":true}`},
	)
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)
	client := startServer(t, cfg)

	listing := client.listTools("user")
	if !strings.Contains(listing, "create_and_notify") {
		t.Errorf("expected pipe to appear in list output, got: %s", listing)
	}
}

func TestPipes_DisabledByDefault_NotListed(t *testing.T) {
	cfg := t.TempDir()
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)
	client := startServer(t, cfg)

	listing := client.listTools("")
	if strings.Contains(listing, "create_and_notify") {
		t.Errorf("pipes should be disabled by default, got list output: %s", listing)
	}
}

func TestPipes_BadPipe_DoesNotPreventGoodPipesLoading(t *testing.T) {
	const goodPipe = `name: good_pipe
description: This one works
steps:
  - id: fetch
    server: github
    tool: create_pull_request
    args:
      title: test
      base: main
`
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "github", mockFixtureDir(t, map[string]string{"create_pull_request": `{"__write_op":true}`}))

	// Bad pipe: no steps (validation error).
	writePipe(t, cfg, "bad_pipe", "name: bad_pipe\nsteps: []\n")
	writePipe(t, cfg, "good_pipe", goodPipe)
	client := startServer(t, cfg)

	listing := client.listTools("user")
	if strings.Contains(listing, "bad_pipe") {
		t.Error("expected bad_pipe not to appear in list")
	}
	if !strings.Contains(listing, "good_pipe") {
		t.Errorf("expected good_pipe to appear despite bad_pipe load error, got: %s", listing)
	}
}

func TestPipes_ParallelKeyRejectedAtLoad(t *testing.T) {
	const parallelPipe = `name: parallel_pipe
description: Tries to use parallel (unsupported)
steps:
  - parallel:
      - id: a
        server: github
        tool: create_pull_request
        args:
          title: test
          base: main
`
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "github", mockFixtureDir(t, map[string]string{"create_pull_request": `{"__write_op":true}`}))
	writePipe(t, cfg, "parallel_pipe", parallelPipe)
	client := startServer(t, cfg)

	// Pipe should not appear in list because it failed validation.
	listing := client.listTools("")
	if strings.Contains(listing, "parallel_pipe") {
		t.Error("expected parallel_pipe to be rejected at load, not appear in list")
	}
}

func TestPipes_ReservedServerName_RejectedAtConfigLoad(t *testing.T) {
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	// Write a server config file named "user" — should be rejected at load.
	dir := filepath.Join(cfg, "servers")
	os.MkdirAll(dir, 0700) //nolint:errcheck
	yaml := fmt.Sprintf("name: user\ncommand: %s\nargs:\n  - --fixtures\n  - %s\n",
		fakemcpBin, t.TempDir())
	os.WriteFile(filepath.Join(dir, "user.yaml"), []byte(yaml), 0600) //nolint:errcheck

	// mini ls reads config — should exit non-zero because "user" is reserved.
	_, _, code := runCLI(t, cfg, "ls")
	if code == 0 {
		t.Error("expected non-zero exit when config has server named 'user'")
	}
}

func TestPipes_CLIList_ShowsNameAndDescription(t *testing.T) {
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "github", mockFixtureDir(t, map[string]string{"create_pull_request": `{"__write_op":true}`}))
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)

	stdout, _, code := runCLI(t, cfg, "pipe", "list")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "create_and_notify") {
		t.Errorf("expected pipe name in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Create PR and notify Slack") {
		t.Errorf("expected pipe description in output, got: %s", stdout)
	}
}

func TestPipes_CLIRequiresOptIn(t *testing.T) {
	cfg := t.TempDir()
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)

	_, stderr, code := runCLI(t, cfg, "pipe", "list")
	if code == 0 {
		t.Fatal("expected pipe list to fail when pipes are disabled")
	}
	if !strings.Contains(stderr, "enable_pipes") {
		t.Errorf("expected enable_pipes hint in stderr, got: %s", stderr)
	}
}

func TestPipes_CLIRun_ExecutesPipeAndPrintsJSON(t *testing.T) {
	cfg := twoServerConfig(t,
		map[string]string{"create_pull_request": `{"__write_op":true}`},
		map[string]string{"post_message": `{"__write_op":true}`},
	)
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)

	stdout, stderr, code := runCLI(t, cfg, "pipe", "run", "create_and_notify", "--args", `{"title":"fix: auth"}`)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var pr pipeResult
	if err := json.Unmarshal([]byte(stdout), &pr); err != nil {
		t.Fatalf("expected JSON output from pipe run, got: %s\nerr: %v", stdout, err)
	}
	if !pr.OK {
		t.Errorf("expected ok=true, got ok=false: %s", pr.Error)
	}
	if pr.Output["pr_url"] == nil {
		t.Errorf("expected pr_url in output, got: %v", pr.Output)
	}
}

func TestPipes_CLIRun_MissingRequiredInput_ExitsNonZero(t *testing.T) {
	cfg := twoServerConfig(t,
		map[string]string{"create_pull_request": `{"__write_op":true}`},
		map[string]string{"post_message": `{"__write_op":true}`},
	)
	writePipe(t, cfg, "create_and_notify", createAndNotifyPipe)

	stdout, _, code := runCLI(t, cfg, "pipe", "run", "create_and_notify")
	// Should either exit 1 (pipe run detected ok=false) or stdout contains ok:false.
	var pr pipeResult
	if json.Unmarshal([]byte(stdout), &pr) == nil && !pr.OK {
		return // pipe ran and reported ok=false — acceptable
	}
	if code != 0 {
		return // exited non-zero — acceptable
	}
	t.Error("expected either ok=false in output or non-zero exit when required input missing")
}

func TestPipes_ThreeStepChain_OutputPropagates(t *testing.T) {
	const chainPipe = `name: three_step
description: Three steps chaining results
inputs:
  title:
    type: string
    required: true
steps:
  - id: create
    server: github
    tool: create_pull_request
    args:
      title: "{{ inputs.title }}"
      base: main
  - id: label
    server: github
    tool: add_labels
    args:
      number: "{{ steps.create.result.number }}"
      labels: [bug]
  - id: notify
    server: slack
    tool: post_message
    args:
      channel: "#eng"
      text: "PR {{ steps.create.result.number }} labeled"
output:
  pr_number: "{{ steps.create.result.number }}"
`
	cfg := twoServerConfig(t,
		map[string]string{
			"create_pull_request": `{"__write_op":true}`,
			"add_labels":          `{"__write_op":true}`,
		},
		map[string]string{"post_message": `{"__write_op":true}`},
	)
	writePipe(t, cfg, "three_step", chainPipe)
	client := startServer(t, cfg)

	text := client.execPipeCall("three_step", map[string]any{"title": "fix: auth"})
	pr := parsePipeResult(t, text)

	if !pr.OK {
		t.Fatalf("expected ok=true for 3-step chain, got: %s", pr.Error)
	}
	if len(pr.Steps) != 3 {
		t.Errorf("expected 3 step results, got %d", len(pr.Steps))
	}
	if pr.Output["pr_number"] == nil {
		t.Errorf("expected pr_number in output, got: %v", pr.Output)
	}
}

func TestPipes_DynamicIf_BasedOnStepResult(t *testing.T) {
	const dynamicIfPipe = `name: dynamic_if_pipe
description: Conditionally labels based on PR number
steps:
  - id: create
    server: github
    tool: create_pull_request
    args:
      title: test
      base: main
  - id: label
    if: "{{ steps.create.result.number > 0 }}"
    server: github
    tool: add_labels
    args:
      number: "{{ steps.create.result.number }}"
      labels: [auto]
output:
  labeled: "{{ steps.create.result.number > 0 }}"
`
	cfg := twoServerConfig(t,
		map[string]string{
			"create_pull_request": `{"__write_op":true}`,
			"add_labels":          `{"__write_op":true}`,
		},
		map[string]string{},
	)
	writePipe(t, cfg, "dynamic_if_pipe", dynamicIfPipe)
	client := startServer(t, cfg)

	text := client.execTool("user", "dynamic_if_pipe", nil)
	pr := parsePipeResult(t, text)

	if !pr.OK {
		t.Fatalf("expected ok=true, got: %s", pr.Error)
	}
	labelStep, ok := findStep(pr.Steps, "label")
	if !ok {
		t.Fatal("expected label step in result")
	}
	if labelStep.Skipped {
		t.Error("expected label step to execute when condition is true")
	}
}

func TestPipes_EnvVarAccess_InArgs(t *testing.T) {
	const envPipe = `name: env_pipe
description: Reads env var in args
steps:
  - id: post
    server: slack
    tool: post_message
    args:
      channel: "#eng"
      text: "{{ env.MINI_TEST_ENV_VAR }}"
`
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writeFakeServer(t, cfg, "slack", mockFixtureDir(t, map[string]string{"post_message": `{"__write_op":true}`}))
	writePipe(t, cfg, "env_pipe", envPipe)

	t.Setenv("MINI_TEST_ENV_VAR", "hello-from-env")
	client := startServer(t, cfg)

	text := client.execTool("user", "env_pipe", nil)
	pr := parsePipeResult(t, text)
	if !pr.OK {
		t.Fatalf("expected ok=true, got: %s", pr.Error)
	}
}

func TestPipes_CLICheck_ReportsOKAndErrors(t *testing.T) {
	const goodPipe = `name: good_pipe
description: Valid
steps:
  - id: s1
    server: github
    tool: create_pull_request
    args:
      title: test
      base: main
`
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writePipe(t, cfg, "good_pipe", goodPipe)
	writePipe(t, cfg, "bad_pipe", "name: bad_pipe\nsteps: []\n")

	stdout, _, codeGood := runCLI(t, cfg, "pipe", "check", "good_pipe")
	if codeGood != 0 {
		t.Errorf("expected exit 0 for valid pipe, got %d", codeGood)
	}
	if !strings.Contains(stdout, "OK") {
		t.Errorf("expected OK in output for valid pipe, got: %s", stdout)
	}

	_, _, codeBad := runCLI(t, cfg, "pipe", "check", "bad_pipe")
	if codeBad == 0 {
		t.Error("expected non-zero exit for invalid pipe")
	}
}

func TestPipes_CLICheck_AllPipes(t *testing.T) {
	const goodPipe = `name: valid_pipe
description: Valid
steps:
  - id: s1
    server: github
    tool: create_pull_request
    args:
      title: test
      base: main
`
	cfg := t.TempDir()
	writePipesConfig(t, cfg)
	writePipe(t, cfg, "valid_pipe", goodPipe)

	stdout, _, code := runCLI(t, cfg, "pipe", "check")
	if code != 0 {
		t.Errorf("expected exit 0 when all pipes valid, got %d\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "OK") {
		t.Errorf("expected OK status, got: %s", stdout)
	}
}
