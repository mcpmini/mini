//go:build evals

package evals

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner holds eval infrastructure and execution configuration.
type Runner struct {
	MiniBin     string
	FakemcpBin  string
	FixturesDir string
	RepoRoot    string
	Modes       map[string]bool // nil means all 7 modes
	Reps        int             // 0 means 1
}

func NewRunner(modes map[string]bool, reps int) (*Runner, error) {
	root, err := findRepoRoot()
	if err != nil {
		return nil, err
	}
	miniBin, err := buildBin(root, "mini", "./cmd/mini")
	if err != nil {
		return nil, fmt.Errorf("build mini: %w", err)
	}
	fakemcpBin, err := buildBin(root, "fakemcp", "./test/fakemcp", "-tags", "integration")
	if err != nil {
		return nil, fmt.Errorf("build fakemcp: %w", err)
	}
	return &Runner{
		MiniBin:     miniBin,
		FakemcpBin:  fakemcpBin,
		FixturesDir: filepath.Join(root, "benchmarks", "fixtures"),
		RepoRoot:    root,
		Modes:       modes,
		Reps:        reps,
	}, nil
}

func FindRepoRoot() (string, error) { return findRepoRoot() }

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not find repo root (no go.mod found)")
}

func buildBin(root, name, pkg string, extraFlags ...string) (string, error) {
	tmp, err := os.MkdirTemp("", "mini-evals-*")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, name)
	args := append([]string{"build"}, extraFlags...)
	args = append(args, "-o", out, pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, b)
	}
	return out, nil
}

func (r *Runner) want(label string) bool { return r.Modes == nil || r.Modes[label] }
func (r *Runner) reps() int {
	if r.Reps > 0 {
		return r.Reps
	}
	return 1
}

func modeLabel(kind string, idx int) string {
	if kind == "direct" {
		return "direct"
	}
	return kind + "-" + fmtLabel[idx]
}

type runSetup struct {
	kind, callDir, workDir string
	idx, rep               int
	cmd                    func() *exec.Cmd
}

// RunEval launches all selected (mode × rep) combos in parallel and collects results.
// Dirs are created before goroutines launch so setup errors are caught synchronously.
func (r *Runner) RunEval(ctx context.Context, env *Env, p EvalParams, task string) (EvalResult, error) {
	setups, err := r.buildSetups(env, p, task)
	if err != nil {
		return EvalResult{}, err
	}
	return r.collectResults(setups), nil
}

func (r *Runner) buildSetups(env *Env, p EvalParams, task string) ([]runSetup, error) {
	reps := r.reps()
	var setups []runSetup

	if r.want("direct") {
		for rep := range reps {
			s, err := r.rawSetup(env, p, task, rep)
			if err != nil {
				return nil, fmt.Errorf("raw setup rep %d: %w", rep+1, err)
			}
			setups = append(setups, s)
		}
	}
	for i := range numFormats {
		for rep := range reps {
			if r.want(modeLabel("mcp", i)) {
				s, err := r.mcpSetup(env, p, task, i, rep)
				if err != nil {
					return nil, fmt.Errorf("mcp-%s setup rep %d: %w", fmtLabel[i], rep+1, err)
				}
				setups = append(setups, s)
			}
			if r.want(modeLabel("cli", i)) {
				s, err := r.cliSetup(env, p, task, i, rep)
				if err != nil {
					return nil, fmt.Errorf("cli-%s setup rep %d: %w", fmtLabel[i], rep+1, err)
				}
				setups = append(setups, s)
			}
		}
	}
	return setups, nil
}

func (r *Runner) rawSetup(env *Env, p EvalParams, task string, rep int) (runSetup, error) {
	callDir := env.DebugDir(fmt.Sprintf("raw-%d", rep+1))
	cfg, err := rawMCPConfig(env, r, p.Servers, callDir)
	if err != nil {
		return runSetup{}, err
	}
	workDir, err := freshWorkDir(env, p.WorkSrcDir)
	if err != nil {
		return runSetup{}, err
	}
	allowed := rawAllowedTools(p.Servers, p.AllowedTools)
	c, w := cfg, workDir
	return runSetup{kind: "direct", idx: 0, rep: rep, callDir: callDir, workDir: workDir,
		cmd: func() *exec.Cmd { return buildClaudeCmd(c, allowed, w, task) },
	}, nil
}

func (r *Runner) mcpSetup(env *Env, p EvalParams, task string, i, rep int) (runSetup, error) {
	callDir := env.DebugDir(fmt.Sprintf("mcp-%s-%d", fmtLabel[i], rep+1))
	cfg, err := miniMCPConfig(env, r, p.Servers, callDir, i)
	if err != nil {
		return runSetup{}, err
	}
	workDir, err := freshWorkDir(env, p.WorkSrcDir)
	if err != nil {
		return runSetup{}, err
	}
	allowed := miniMCPAllowedTools(p.AllowedTools)
	c, w := cfg, workDir
	return runSetup{kind: "mcp", idx: i, rep: rep, callDir: callDir, workDir: workDir,
		cmd: func() *exec.Cmd { return buildClaudeCmd(c, allowed, w, task) },
	}, nil
}

func (r *Runner) cliSetup(env *Env, p EvalParams, task string, i, rep int) (runSetup, error) {
	callDir := env.DebugDir(fmt.Sprintf("cli-%s-%d", fmtLabel[i], rep+1))
	cfgDir, err := miniCLIConfigDir(env, r, p.Servers, callDir, i)
	if err != nil {
		return runSetup{}, err
	}
	wrapDir, err := writeMiniWrapper(env, r.MiniBin, cfgDir)
	if err != nil {
		return runSetup{}, err
	}
	workDir, err := freshWorkDir(env, p.WorkSrcDir)
	if err != nil {
		return runSetup{}, err
	}
	allowed := cliAllowedTools(p.AllowedTools)
	wrap, w := wrapDir, workDir
	return runSetup{kind: "cli", idx: i, rep: rep, callDir: callDir, workDir: workDir,
		cmd: func() *exec.Cmd { return buildClaudeCLICmd(wrap, allowed, w, task) },
	}, nil
}

type jobResult struct {
	kind string
	idx  int
	rep  int
	r    ClaudeResult
}

func (r *Runner) collectResults(setups []runSetup) EvalResult {
	ch := make(chan jobResult, len(setups))
	for _, s := range setups {
		s := s
		go func() {
			output, err := runClaudeCmd(s.cmd(), s.callDir)
			if err == nil && isRateLimited(output) {
				err = fmt.Errorf("claude rate limited: %s", strings.TrimSpace(output))
			}
			result := parseClaudeResult(output)
			result.Ran = true
			result.Err = err
			result.WorkDir = s.workDir
			result.CallLogDir = s.callDir
			result.RawOutputPath = saveOutput(s.callDir, "claude-output.json", output)
			ch <- jobResult{s.kind, s.idx, s.rep, result}
		}()
	}

	reps := r.reps()
	directRuns := make([]ClaudeResult, reps)
	mcpRuns := [numFormats][]ClaudeResult{}
	cliRuns := [numFormats][]ClaudeResult{}
	for i := range numFormats {
		mcpRuns[i] = make([]ClaudeResult, reps)
		cliRuns[i] = make([]ClaudeResult, reps)
	}
	for range len(setups) {
		res := <-ch
		switch res.kind {
		case "direct":
			directRuns[res.rep] = res.r
		case "mcp":
			mcpRuns[res.idx][res.rep] = res.r
		case "cli":
			cliRuns[res.idx][res.rep] = res.r
		}
	}

	var eval EvalResult
	if r.want("direct") {
		eval.Direct = RunStats{Runs: directRuns}
	}
	for i := range numFormats {
		if r.want(modeLabel("mcp", i)) {
			eval.MCP[i] = RunStats{Runs: mcpRuns[i]}
		}
		if r.want(modeLabel("cli", i)) {
			eval.CLI[i] = RunStats{Runs: cliRuns[i]}
		}
	}
	return eval
}
