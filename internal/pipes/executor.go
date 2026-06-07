package pipes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/expr-lang/expr/vm"

	"github.com/mcpmini/mini/internal/config"
)

// CallerFunc dispatches a tool call to an upstream server and returns the raw JSON response.
type CallerFunc func(ctx context.Context, server, tool string, args map[string]any) (json.RawMessage, error)

// StepResult records the outcome of one executed step.
type StepResult struct {
	ID              string `json:"id"`
	OK              bool   `json:"ok,omitempty"`
	Skipped         bool   `json:"skipped,omitempty"`
	Silent          bool   `json:"silent,omitempty"`
	ContinueOnError bool   `json:"continue_on_error,omitempty"`
	Error           string `json:"error,omitempty"`
}

// Result is the structured output of a pipe execution.
type Result struct {
	Server        string         `json:"server"`
	Tool          string         `json:"tool"`
	OK            bool           `json:"ok"`
	Output        map[string]any `json:"output,omitempty"`
	Steps         []StepResult   `json:"steps"`
	Error         string         `json:"error,omitempty"`
	FailedStep    string         `json:"failed_step,omitempty"`
	PartialOutput map[string]any `json:"partial_output,omitempty"`
	LatencyMs     int64          `json:"latency_ms"`
}

// Execute runs all steps of the compiled pipe and returns the result.
func (cp *CompiledPipe) Execute(ctx context.Context, inputs map[string]any, caller CallerFunc) (*Result, error) {
	start := time.Now()
	result := &Result{
		Server: config.UserServerName,
		Tool:   cp.Config.Name,
	}
	if err := validateInputs(cp.Config, inputs); err != nil {
		result.Error = err.Error()
		result.LatencyMs = time.Since(start).Milliseconds()
		return result, nil
	}
	state := make(map[string]any)
	envMap := buildEnvMap()
	ok, failedStep := cp.runSteps(ctx, inputs, state, envMap, caller, result)
	result.LatencyMs = time.Since(start).Milliseconds()
	if !ok {
		result.FailedStep = failedStep
		result.PartialOutput = cp.evalOutput(inputs, state, envMap)
		return result, nil
	}
	result.OK = true
	result.Output = cp.evalOutput(inputs, state, envMap)
	return result, nil
}

func validateInputs(pipe config.PipeConfig, inputs map[string]any) error {
	for name, schema := range pipe.Inputs {
		if schema.Required {
			if _, ok := inputs[name]; !ok {
				return fmt.Errorf("missing required input: %s", name)
			}
		}
	}
	return nil
}

func buildEnvMap() map[string]string {
	envMap := make(map[string]string)
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	return envMap
}

func (cp *CompiledPipe) runSteps(ctx context.Context, inputs map[string]any, state map[string]any, envMap map[string]string, caller CallerFunc, result *Result) (bool, string) {
	for _, step := range cp.Config.Steps {
		cs := cp.stepExprs[step.ID]
		env := buildRuntimeEnv(inputs, state, envMap)
		skipped, err := cp.runOneStep(ctx, step, cs, env, state, caller)
		sr := makeStepResult(step, skipped, err)
		result.Steps = append(result.Steps, sr)
		if err != nil && !step.ContinueOnError {
			result.Error = fmt.Sprintf("step %q failed: %s", step.ID, err)
			return false, step.ID
		}
	}
	return true, ""
}

func (cp *CompiledPipe) runOneStep(ctx context.Context, step config.StepConfig, cs *compiledStep, env map[string]any, state map[string]any, caller CallerFunc) (skipped bool, err error) {
	if cs != nil && cs.ifProg != nil {
		val, evalErr := runProg(cs.ifProg, env)
		if evalErr != nil {
			return false, fmt.Errorf("if condition: %w", evalErr)
		}
		if !isTruthy(val) {
			state[step.ID] = nil
			return true, nil
		}
	}
	if len(step.Set) > 0 {
		return false, cp.runSetStep(step, cs, env, state)
	}
	return false, cp.runToolStep(ctx, step, cs, env, state, caller)
}

func (cp *CompiledPipe) runSetStep(step config.StepConfig, cs *compiledStep, env map[string]any, state map[string]any) error {
	result := map[string]any{"ok": true}
	for name, prog := range cs.setProgs {
		val, err := runProg(prog, env)
		if err != nil {
			return fmt.Errorf("set.%s: %w", name, err)
		}
		result[name] = val
	}
	state[step.ID] = result
	return nil
}

func (cp *CompiledPipe) runToolStep(ctx context.Context, step config.StepConfig, cs *compiledStep, env map[string]any, state map[string]any, caller CallerFunc) error {
	args, err := interpolateArgs(step.Args, cs, env)
	if err != nil {
		return fmt.Errorf("arg interpolation: %w", err)
	}
	raw, callErr := caller(ctx, step.Server, step.Tool, args)
	if callErr != nil {
		state[step.ID] = map[string]any{"ok": false, "error": callErr.Error()}
		return callErr
	}
	stepState := map[string]any{"ok": true, "result": parseToolResult(raw)}
	state[step.ID] = stepState
	return nil
}

func interpolateArgs(args map[string]any, cs *compiledStep, env map[string]any) (map[string]any, error) {
	if cs == nil || len(cs.argProgs) == 0 {
		return args, nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	for key, segs := range cs.argProgs {
		val, err := evalSegments(segs, env)
		if err != nil {
			return nil, fmt.Errorf("args.%s: %w", key, err)
		}
		out[key] = val
	}
	return out, nil
}

func evalSegments(segs []*exprSegment, env map[string]any) (any, error) {
	if len(segs) == 1 && segs[0].prog != nil {
		return runProg(segs[0].prog, env)
	}
	var sb strings.Builder
	for _, seg := range segs {
		if seg.prog == nil {
			sb.WriteString(seg.literal)
			continue
		}
		val, err := runProg(seg.prog, env)
		if err != nil {
			return nil, err
		}
		sb.WriteString(fmt.Sprintf("%v", val))
	}
	return sb.String(), nil
}

func (cp *CompiledPipe) evalOutput(inputs map[string]any, state map[string]any, envMap map[string]string) map[string]any {
	if len(cp.outputExprs) == 0 {
		return nil
	}
	env := buildRuntimeEnv(inputs, state, envMap)
	out := make(map[string]any, len(cp.outputExprs))
	for field, segs := range cp.outputExprs {
		val, err := evalSegments(segs, env)
		if err != nil {
			out[field] = nil
		} else {
			out[field] = val
		}
	}
	return out
}

func buildRuntimeEnv(inputs map[string]any, state map[string]any, envMap map[string]string) map[string]any {
	return map[string]any{
		"inputs": inputs,
		"steps":  state,
		"env":    envMap,
	}
}

func parseToolResult(raw json.RawMessage) any {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return out
}

func makeStepResult(step config.StepConfig, skipped bool, err error) StepResult {
	sr := StepResult{
		ID:              step.ID,
		Silent:          step.Silent,
		ContinueOnError: step.ContinueOnError,
	}
	if skipped {
		sr.Skipped = true
	} else if err != nil {
		sr.OK = false
		sr.Error = err.Error()
	} else {
		sr.OK = true
	}
	return sr
}

func runProg(prog *vm.Program, env any) (any, error) {
	v := vm.VM{}
	return v.Run(prog, env)
}

func isTruthy(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case string:
		return v != ""
	}
	return true
}
