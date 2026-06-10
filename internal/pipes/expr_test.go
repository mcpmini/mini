//go:build test

package pipes_test

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/pipes"
)

func TestCompile_ValidPipe(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Inputs: map[string]config.InputSchema{
			"title": {Type: "string"},
		},
		Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr", Args: map[string]any{
				"title": "{{ inputs.title }}",
			}},
		},
		Output: map[string]string{
			"result": "{{ steps.step1.result.number }}",
		},
	}
	if _, err := pipes.Compile(pipe); err != nil {
		t.Fatalf("expected no compile error: %v", err)
	}
}

func TestCompile_InvalidIfExpr(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Steps: []config.StepConfig{
			{ID: "s1", Server: "gh", Tool: "create_pr", If: "{{ %%invalid%% }}"},
		},
	}
	_, err := pipes.Compile(pipe)
	if err == nil {
		t.Fatal("expected compile error for invalid if expression")
	}
}

func TestCompile_InvalidArgExpr(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Steps: []config.StepConfig{
			{ID: "s1", Server: "gh", Tool: "create_pr", Args: map[string]any{
				"body": "PR: {{ %%bad%% }}",
			}},
		},
	}
	_, err := pipes.Compile(pipe)
	if err == nil {
		t.Fatal("expected compile error for invalid arg expression")
	}
}

func TestCompile_InvalidSetExpr(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Steps: []config.StepConfig{
			{ID: "s1", Set: map[string]string{"x": "{{ %%bad%% }}"}},
		},
	}
	_, err := pipes.Compile(pipe)
	if err == nil {
		t.Fatal("expected compile error for invalid set expression")
	}
}

func TestCompile_InvalidOutputExpr(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Steps: []config.StepConfig{
			{ID: "s1", Server: "gh", Tool: "t"},
		},
		Output: map[string]string{
			"x": "{{ %%bad%% }}",
		},
	}
	_, err := pipes.Compile(pipe)
	if err == nil {
		t.Fatal("expected compile error for invalid output expression")
	}
}

func TestCompile_MultipleErrors_AllReported(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Steps: []config.StepConfig{
			{ID: "s1", Server: "gh", Tool: "t", If: "{{ %%bad1%% }}"},
			{ID: "s2", Server: "gh", Tool: "t", If: "{{ %%bad2%% }}"},
		},
	}
	_, err := pipes.Compile(pipe)
	if err == nil {
		t.Fatal("expected compile error")
	}
}

func TestCompile_NonStringArgSkipped(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Steps: []config.StepConfig{
			{ID: "s1", Server: "gh", Tool: "create_pr", Args: map[string]any{
				"count": 42,
				"flag":  true,
			}},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatalf("expected no error for non-string args: %v", err)
	}
	result := cp.Execute(context.Background(), nil, makeCaller(nil))
	if !result.OK {
		t.Errorf("expected OK=true, got error: %s", result.Error)
	}
}

func TestCompile_BracesStripped_IfExpr(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Steps: []config.StepConfig{
			{ID: "skip", Server: "gh", Tool: "t", If: "{{ false }}"},
			{ID: "run", Server: "gh", Tool: "t"},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	result := cp.Execute(context.Background(), nil, makeCaller(nil))
	if !result.OK {
		t.Fatalf("expected OK=true: %s", result.Error)
	}
	skippedStep := findStep(result.Steps, "skip")
	if skippedStep == nil || !skippedStep.Skipped {
		t.Error("expected 'skip' step to be skipped")
	}
}

func TestCompile_SetExprWithoutBraces(t *testing.T) {
	pipe := config.PipeConfig{
		Name: "p",
		Steps: []config.StepConfig{
			{ID: "s", Set: map[string]string{"x": "true"}},
			{ID: "run", Server: "gh", Tool: "t"},
		},
	}
	cp, err := pipes.Compile(pipe)
	if err != nil {
		t.Fatal(err)
	}
	result := cp.Execute(context.Background(), nil, makeCaller(nil))
	if !result.OK {
		t.Fatalf("expected OK=true: %s", result.Error)
	}
}
