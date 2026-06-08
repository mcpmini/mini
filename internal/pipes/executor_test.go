//go:build test

package pipes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/pipes"
)

type fakeCallerResponses map[string]json.RawMessage

func makeCaller(responses fakeCallerResponses) pipes.CallerFunc {
	return func(_ context.Context, server, tool string, _ map[string]any) (json.RawMessage, error) {
		key := server + "." + tool
		if raw, ok := responses[key]; ok {
			return raw, nil
		}
		return json.RawMessage(`{"result": "ok"}`), nil
	}
}

func okResp(fields map[string]any) json.RawMessage {
	b, _ := json.Marshal(fields)
	return json.RawMessage(b)
}

func TestExecutor_AllStepsSucceed(t *testing.T) {
	pipe := config.PipeConfig{
		Name:        "test_pipe",
		Description: "test",
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr"},
			{ID: "step2", Server: "slack", Tool: "post"},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	caller := makeCaller(fakeCallerResponses{
		"gh.create_pr": okResp(map[string]any{"number": 42}),
		"slack.post":   okResp(map[string]any{"ok": true}),
	})
	result := cp.Execute(context.Background(), nil, caller)
	if !result.OK {
		t.Fatalf("expected OK=true, got error: %s", result.Error)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 step results, got %d", len(result.Steps))
	}
	for _, s := range result.Steps {
		if !s.OK {
			t.Errorf("step %s: expected OK", s.ID)
		}
	}
}

func TestExecutor_MiddleStepFails_Aborts(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "abort_pipe",
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr"},
			{ID: "fail", Server: "gh", Tool: "bad_tool"},
			{ID: "step3", Server: "slack", Tool: "post"},
		},
	}
	cp, _ := pipes.Compile(pipe)

	var step3Called bool
	caller := func(_ context.Context, server, tool string, _ map[string]any) (json.RawMessage, error) {
		if tool == "bad_tool" {
			return nil, fmt.Errorf("upstream error")
		}
		if tool == "post" {
			step3Called = true
		}
		return json.RawMessage(`{}`), nil
	}
	result := cp.Execute(context.Background(), nil, caller)
	if result.OK {
		t.Fatal("expected OK=false")
	}
	if result.FailedStep != "fail" {
		t.Errorf("FailedStep = %q, want %q", result.FailedStep, "fail")
	}
	if step3Called {
		t.Error("step3 should not have been called after failure")
	}
}

func TestExecutor_ContinueOnError(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "continue_pipe",
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr"},
			{ID: "notify", Server: "slack", Tool: "post", ContinueOnError: true},
			{ID: "step3", Server: "gh", Tool: "add_label"},
		},
	}
	cp, _ := pipes.Compile(pipe)

	var step3Called bool
	caller := func(_ context.Context, server, tool string, _ map[string]any) (json.RawMessage, error) {
		if tool == "post" {
			return nil, fmt.Errorf("slack down")
		}
		if tool == "add_label" {
			step3Called = true
		}
		return json.RawMessage(`{}`), nil
	}
	result := cp.Execute(context.Background(), nil, caller)
	if !result.OK {
		t.Fatalf("expected OK=true with continue_on_error, got error: %s", result.Error)
	}
	if !step3Called {
		t.Error("step3 should have been called after continue_on_error step")
	}
	notifyResult := findStep(result.Steps, "notify")
	if notifyResult == nil || notifyResult.Error == "" {
		t.Error("notify step should have an error")
	}
}

func TestExecutor_SkippedStep(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "skip_pipe",
		Steps: []config.StepConfig{
			{ID: "cond", Server: "gh", Tool: "create_pr", If: "false"},
			{ID: "step2", Server: "slack", Tool: "post"},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	var condCalled bool
	caller := func(_ context.Context, server, tool string, _ map[string]any) (json.RawMessage, error) {
		if tool == "create_pr" {
			condCalled = true
		}
		return json.RawMessage(`{}`), nil
	}
	result := cp.Execute(context.Background(), nil, caller)
	if !result.OK {
		t.Fatalf("expected OK=true, error: %s", result.Error)
	}
	if condCalled {
		t.Error("cond step should have been skipped")
	}
	condStep := findStep(result.Steps, "cond")
	if condStep == nil || !condStep.Skipped {
		t.Error("cond step should be marked skipped")
	}
}

func TestExecutor_SetStep(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "set_pipe",
		Steps: []config.StepConfig{
			{ID: "classify", Set: map[string]string{"has_docs": "true", "value": "42"}},
			{ID: "use", Server: "gh", Tool: "create_pr"},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	caller := makeCaller(nil)
	result := cp.Execute(context.Background(), nil, caller)
	if !result.OK {
		t.Fatalf("expected OK=true, error: %s", result.Error)
	}
	classifyStep := findStep(result.Steps, "classify")
	if classifyStep == nil || !classifyStep.OK {
		t.Error("classify step should be OK")
	}
}

func TestExecutor_OutputBlock(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "output_pipe",
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
	caller := makeCaller(fakeCallerResponses{
		"gh.create_pr": okResp(map[string]any{"number": 99}),
	})
	result := cp.Execute(context.Background(), nil, caller)
	if !result.OK {
		t.Fatalf("expected OK=true, error: %s", result.Error)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output")
	}
	num, _ := result.Output["pr_number"].(float64)
	if int(num) != 99 {
		t.Errorf("pr_number = %v, want 99", result.Output["pr_number"])
	}
}

func TestExecutor_MissingRequiredInput(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "input_pipe",
		Inputs: map[string]config.InputSchema{
			"title": {Type: "string", Required: true},
		},
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr"},
		},
	}
	cp, _ := pipes.Compile(pipe)
	result := cp.Execute(context.Background(), map[string]any{}, makeCaller(nil))
	if result.OK {
		t.Fatal("expected OK=false for missing required input")
	}
	if result.Error == "" {
		t.Error("expected non-empty error for missing required input")
	}
}

func TestExecutor_InputInterpolation(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "interp_pipe",
		Inputs: map[string]config.InputSchema{
			"title": {Type: "string"},
		},
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr", Args: map[string]any{
				"title": "PR: {{ inputs.title }}",
			}},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	var capturedTitle string
	caller := func(_ context.Context, server, tool string, args map[string]any) (json.RawMessage, error) {
		capturedTitle, _ = args["title"].(string)
		return json.RawMessage(`{}`), nil
	}
	result := cp.Execute(context.Background(), map[string]any{"title": "fix auth"}, caller)
	if !result.OK {
		t.Fatalf("expected OK=true, error: %s", result.Error)
	}
	if capturedTitle != "PR: fix auth" {
		t.Errorf("captured title = %q, want %q", capturedTitle, "PR: fix auth")
	}
}

func findStep(steps []pipes.StepResult, id string) *pipes.StepResult {
	for i := range steps {
		if steps[i].ID == id {
			return &steps[i]
		}
	}
	return nil
}
