package pipes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/mcpmini/mini/internal/config"
)

// exprPattern matches {{ expression }} in string values.
var exprPattern = regexp.MustCompile(`\{\{(.+?)\}\}`)

// CompiledPipe holds a PipeConfig with all expressions pre-compiled.
type CompiledPipe struct {
	Config      config.PipeConfig
	stepExprs   map[string]*compiledStep
	outputExprs map[string][]*exprSegment
}

type compiledStep struct {
	ifProg  *vm.Program
	setProgs map[string]*vm.Program
	argProgs map[string][]*exprSegment // key → parsed segments
}

// exprSegment is one piece of an interpolated string (literal or expression).
type exprSegment struct {
	literal string
	prog    *vm.Program // nil for literal segments
}

// Compile pre-compiles all expressions in a PipeConfig.
func Compile(pipe config.PipeConfig) (*CompiledPipe, error) {
	cp := &CompiledPipe{
		Config:      pipe,
		stepExprs:   make(map[string]*compiledStep),
		outputExprs: make(map[string][]*exprSegment),
	}
	knownStepIDs := make(map[string]bool)
	var errs []error
	for _, step := range pipe.Steps {
		env := buildCompileEnv(pipe.Inputs, knownStepIDs)
		cs, err := compileStep(step, env)
		if err != nil {
			errs = append(errs, fmt.Errorf("step %q: %w", step.ID, err))
		} else {
			cp.stepExprs[step.ID] = cs
		}
		knownStepIDs[step.ID] = true
	}
	env := buildCompileEnv(pipe.Inputs, knownStepIDs)
	for field, exprStr := range pipe.Output {
		segs, err := compileStringExpr(exprStr, env)
		if err != nil {
			errs = append(errs, fmt.Errorf("output.%s: %w", field, err))
		} else {
			cp.outputExprs[field] = segs
		}
	}
	return cp, errors.Join(errs...)
}

func buildCompileEnv(inputs map[string]config.InputSchema, knownStepIDs map[string]bool) map[string]any {
	stepsMap := make(map[string]any, len(knownStepIDs))
	for id := range knownStepIDs {
		stepsMap[id] = map[string]any{}
	}
	inputsMap := make(map[string]any, len(inputs))
	for k := range inputs {
		inputsMap[k] = nil
	}
	return map[string]any{
		"inputs": inputsMap,
		"steps":  stepsMap,
		"env":    map[string]string{},
	}
}

func compileStep(step config.StepConfig, env map[string]any) (*compiledStep, error) {
	cs := &compiledStep{
		setProgs: make(map[string]*vm.Program),
		argProgs: make(map[string][]*exprSegment),
	}
	var errs []error
	if step.If != "" {
		prog, err := compileExpr(step.If, env)
		if err != nil {
			errs = append(errs, fmt.Errorf("if: %w", err))
		} else {
			cs.ifProg = prog
		}
	}
	for name, exprStr := range step.Set {
		prog, err := compileExpr(exprStr, env)
		if err != nil {
			errs = append(errs, fmt.Errorf("set.%s: %w", name, err))
		} else {
			cs.setProgs[name] = prog
		}
	}
	for key, val := range step.Args {
		strVal, ok := val.(string)
		if !ok {
			continue
		}
		segs, err := compileStringExpr(strVal, env)
		if err != nil {
			errs = append(errs, fmt.Errorf("args.%s: %w", key, err))
		} else if hasExprSegment(segs) {
			cs.argProgs[key] = segs
		}
	}
	return cs, errors.Join(errs...)
}

func hasExprSegment(segs []*exprSegment) bool {
	for _, s := range segs {
		if s.prog != nil {
			return true
		}
	}
	return false
}

// compileStringExpr parses a string that may contain {{ expr }} placeholders
// and returns a slice of segments (literal text or compiled program).
func compileStringExpr(s string, env map[string]any) ([]*exprSegment, error) {
	matches := exprPattern.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return []*exprSegment{{literal: s}}, nil
	}
	var (
		segs []exprSegment
		pos  int
		errs []error
	)
	for _, m := range matches {
		if m[0] > pos {
			segs = append(segs, exprSegment{literal: s[pos:m[0]]})
		}
		exprText := strings.TrimSpace(s[m[2]:m[3]])
		prog, err := compileExpr(exprText, env)
		if err != nil {
			errs = append(errs, err)
		} else {
			segs = append(segs, exprSegment{prog: prog})
		}
		pos = m[1]
	}
	if pos < len(s) {
		segs = append(segs, exprSegment{literal: s[pos:]})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	out := make([]*exprSegment, len(segs))
	for i := range segs {
		out[i] = &segs[i]
	}
	return out, nil
}

func compileExpr(exprStr string, env map[string]any) (*vm.Program, error) {
	return expr.Compile(exprStr,
		expr.Env(env),
		expr.AllowUndefinedVariables(),
		expr.AsAny(),
	)
}
