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

func TestExecutor_ContextCancellation(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "cancel_pipe",
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "slow_tool"},
		},
	}
	cp, _ := pipes.Compile(pipe)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	caller := func(ctx context.Context, _, _ string, _ map[string]any) (json.RawMessage, error) {
		return nil, ctx.Err()
	}
	result := cp.Execute(ctx, nil, caller)
	if result.OK {
		t.Fatal("expected OK=false after context cancellation")
	}
	if result.Error == "" {
		t.Error("expected non-empty error for cancelled context")
	}
}

func TestExecutor_IfConditionEvalError(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "eval_err_pipe",
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "t"},
			{ID: "guarded", Server: "gh", Tool: "t", If: "{{ steps.step1.result[0] }}"},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	result := cp.Execute(context.Background(), nil, makeCaller(nil))
	if result.OK {
		t.Fatal("expected runtime expression error to fail the pipe")
	}
	if result.FailedStep != "guarded" {
		t.Errorf("FailedStep = %q, want guarded", result.FailedStep)
	}
}

func TestExecutor_StepResultAvailableToNextStep(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "chain_pipe",
		Inputs: map[string]config.InputSchema{},
		Steps: []config.StepConfig{
			{ID: "first", Server: "gh", Tool: "create_pr"},
			{ID: "second", Server: "gh", Tool: "post", Args: map[string]any{
				"pr": "{{ steps.first.result.number }}",
			}},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	var capturedPR any
	caller := func(_ context.Context, _, tool string, args map[string]any) (json.RawMessage, error) {
		if tool == "post" {
			capturedPR = args["pr"]
		}
		return json.RawMessage(`{"number": 55}`), nil
	}
	result := cp.Execute(context.Background(), nil, caller)
	if !result.OK {
		t.Fatalf("expected OK=true: %s", result.Error)
	}
	if pr, _ := capturedPR.(float64); int(pr) != 55 {
		t.Errorf("chained pr arg = %v, want 55", capturedPR)
	}
}

func TestExecutor_MultipleOptionalInputs_WithDefaults(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "optional_pipe",
		Inputs: map[string]config.InputSchema{
			"title": {Type: "string", Required: true},
			"draft": {Type: "bool", Required: false, Default: false},
			"label": {Type: "string", Required: false, Default: "auto"},
		},
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr"},
		},
		Output: map[string]string{
			"draft": "{{ inputs.draft }}",
			"label": "{{ inputs.label }}",
		},
	}
	cp, _ := pipes.Compile(pipe)
	result := cp.Execute(context.Background(), map[string]any{"title": "fix"}, makeCaller(nil))
	if !result.OK {
		t.Fatalf("expected OK=true with optional inputs: %s", result.Error)
	}
	if result.Output["draft"] != false {
		t.Errorf("output.draft = %v, want false (default applied)", result.Output["draft"])
	}
	if result.Output["label"] != "auto" {
		t.Errorf("output.label = %v, want \"auto\" (default applied)", result.Output["label"])
	}
}

func TestExecutor_ProvidedInputOverridesDefault(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "override_pipe",
		Inputs: map[string]config.InputSchema{
			"label": {Type: "string", Required: false, Default: "auto"},
		},
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr"},
		},
		Output: map[string]string{
			"label": "{{ inputs.label }}",
		},
	}
	cp, _ := pipes.Compile(pipe)
	result := cp.Execute(context.Background(), map[string]any{"label": "manual"}, makeCaller(nil))
	if !result.OK {
		t.Fatalf("expected OK=true: %s", result.Error)
	}
	if result.Output["label"] != "manual" {
		t.Errorf("output.label = %v, want \"manual\" (caller value should win)", result.Output["label"])
	}
}

func TestExecutor_PartialOutput_OnFailure(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "partial_pipe",
		Steps: []config.StepConfig{
			{ID: "ok_step", Server: "gh", Tool: "create_pr"},
			{ID: "fail_step", Server: "gh", Tool: "bad"},
		},
		Output: map[string]string{
			"pr_number": "{{ steps.ok_step.result.number }}",
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	caller := func(_ context.Context, _, tool string, _ map[string]any) (json.RawMessage, error) {
		if tool == "bad" {
			return nil, fmt.Errorf("fail")
		}
		return json.RawMessage(`{"number": 11}`), nil
	}
	result := cp.Execute(context.Background(), nil, caller)
	if result.OK {
		t.Fatal("expected OK=false")
	}
	if result.PartialOutput == nil {
		t.Fatal("expected partial_output on failure")
	}
	if pr, _ := result.PartialOutput["pr_number"].(float64); int(pr) != 11 {
		t.Errorf("partial pr_number = %v, want 11", result.PartialOutput["pr_number"])
	}
}

func TestExecutor_OutputExprError_RecordedInOutputErrors(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "bad_output_pipe",
		Steps: []config.StepConfig{
			{ID: "s1", Server: "gh", Tool: "create_pr"},
		},
		Output: map[string]string{
			"good": "{{ steps.s1.result.number }}",
			"bad":  "{{ steps.s1.result[0] }}",
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	caller := func(_ context.Context, _, _ string, _ map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"number": 11}`), nil
	}
	result := cp.Execute(context.Background(), nil, caller)
	if !result.OK {
		t.Fatalf("expected OK=true: %s", result.Error)
	}
	if pr, _ := result.Output["good"].(float64); int(pr) != 11 {
		t.Errorf("output.good = %v, want 11", result.Output["good"])
	}
	if _, ok := result.Output["bad"]; ok {
		t.Errorf("output.bad should be absent on eval error, got %v", result.Output["bad"])
	}
	if result.OutputErrors["bad"] == "" {
		t.Error("expected OutputErrors[\"bad\"] to be populated")
	}
}

func TestExecutor_Latency_Recorded(t *testing.T) {
	pipe := config.PipeConfig{
		Name:  "latency_pipe",
		Steps: []config.StepConfig{{ID: "s1", Server: "gh", Tool: "t"}},
	}
	cp, _ := pipes.Compile(pipe)
	result := cp.Execute(context.Background(), nil, makeCaller(nil))
	if result.LatencyMs < 0 {
		t.Errorf("latency_ms should be non-negative, got %d", result.LatencyMs)
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
