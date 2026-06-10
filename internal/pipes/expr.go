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
	ifProg   *vm.Program
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
	errs := cp.compileSteps(pipe)
	errs = append(errs, cp.compileOutput(pipe)...)
	return cp, errors.Join(errs...)
}

func (cp *CompiledPipe) compileSteps(pipe config.PipeConfig) []error {
	var errs []error
	knownStepIDs := make(map[string]bool)
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
	return errs
}

func (cp *CompiledPipe) compileOutput(pipe config.PipeConfig) []error {
	var errs []error
	env := buildCompileEnv(pipe.Inputs, knownStepIDsFrom(pipe))
	for field, exprStr := range pipe.Output {
		segs, err := compileStringExpr(exprStr, env)
		if err != nil {
			errs = append(errs, fmt.Errorf("output.%s: %w", field, err))
		} else {
			cp.outputExprs[field] = segs
		}
	}
	return errs
}

func knownStepIDsFrom(pipe config.PipeConfig) map[string]bool {
	ids := make(map[string]bool, len(pipe.Steps))
	for _, s := range pipe.Steps {
		ids[s.ID] = true
	}
	return ids
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
	errs = append(errs, compileIfExpr(step, env, cs)...)
	errs = append(errs, compileSetExprs(step, env, cs)...)
	errs = append(errs, compileArgExprs(step, env, cs)...)
	return cs, errors.Join(errs...)
}

func compileIfExpr(step config.StepConfig, env map[string]any, cs *compiledStep) []error {
	if step.If == "" {
		return nil
	}
	prog, err := compileExpr(stripBraces(step.If), env)
	if err != nil {
		return []error{fmt.Errorf("if: %w", err)}
	}
	cs.ifProg = prog
	return nil
}

func compileSetExprs(step config.StepConfig, env map[string]any, cs *compiledStep) []error {
	var errs []error
	for name, exprStr := range step.Set {
		prog, err := compileExpr(stripBraces(exprStr), env)
		if err != nil {
			errs = append(errs, fmt.Errorf("set.%s: %w", name, err))
		} else {
			cs.setProgs[name] = prog
		}
	}
	return errs
}

func compileArgExprs(step config.StepConfig, env map[string]any, cs *compiledStep) []error {
	var errs []error
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
	return errs
}

func hasExprSegment(segs []*exprSegment) bool {
	for _, s := range segs {
		if s.prog != nil {
			return true
		}
	}
	return false
}

func compileStringExpr(s string, env map[string]any) ([]*exprSegment, error) {
	matches := exprPattern.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return []*exprSegment{{literal: s}}, nil
	}
	segs, errs := parseExprSegments(s, matches, env)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	out := make([]*exprSegment, len(segs))
	for i := range segs {
		out[i] = &segs[i]
	}
	return out, nil
}

func parseExprSegments(s string, matches [][]int, env map[string]any) ([]exprSegment, []error) {
	var (
		segs []exprSegment
		errs []error
		pos  int
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
	return segs, errs
}

func compileExpr(exprStr string, env map[string]any) (*vm.Program, error) {
	return expr.Compile(exprStr,
		expr.Env(env),
		expr.AllowUndefinedVariables(),
		expr.AsAny(),
	)
}

// stripBraces removes {{ }} delimiters from if/set expressions. The pipe spec
// uses {{ expr }} everywhere for consistency, but the underlying compiler
// expects bare expressions without the template delimiters.
func stripBraces(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
		return strings.TrimSpace(s[2 : len(s)-2])
	}
	return s
}
