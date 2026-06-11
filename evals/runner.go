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
	bins, err := buildEvalBins(root)
	if err != nil {
		return nil, err
	}
	return buildRunner(root, bins, modes, reps), nil
}

type evalBins struct {
	MiniBin    string
	FakemcpBin string
}

func buildRunner(root string, bins evalBins, modes map[string]bool, reps int) *Runner {
	return &Runner{
		MiniBin:     bins.MiniBin,
		FakemcpBin:  bins.FakemcpBin,
		FixturesDir: filepath.Join(root, "benchmarks", "fixtures"),
		RepoRoot:    root,
		Modes:       modes,
		Reps:        reps,
	}
}

func buildEvalBins(root string) (evalBins, error) {
	miniBin, err := buildBin(root, "mini", "./cmd/mini")
	if err != nil {
		return evalBins{}, fmt.Errorf("build mini: %w", err)
	}
	fakemcpBin, err := buildBin(root, "fakemcp", "./test/fakemcp", "-tags", "integration")
	if err != nil {
		return evalBins{}, fmt.Errorf("build fakemcp: %w", err)
	}
	return evalBins{MiniBin: miniBin, FakemcpBin: fakemcpBin}, nil
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

// evalCtx bundles the inputs that pass unchanged through the setup-building chain.
type evalCtx struct {
	Env    *Env
	Params EvalParams
	Task   string
}

// RunEval launches all selected (mode × rep) combos in parallel and collects results.
// Dirs are created before goroutines launch so setup errors are caught synchronously.
func (r *Runner) RunEval(ctx context.Context, ec evalCtx) (EvalResult, error) {
	setups, err := r.buildSetups(ec)
	if err != nil {
		return EvalResult{}, err
	}
	return r.collectResults(setups), nil
}

func (r *Runner) buildSetups(ec evalCtx) ([]runSetup, error) {
	reps := r.reps()
	setups, err := r.buildDirectSetups(ec, reps)
	if err != nil {
		return nil, err
	}
	for i := range numFormats {
		ss, err := r.buildFormatSetups(ec, i, reps)
		if err != nil {
			return nil, err
		}
		setups = append(setups, ss...)
	}
	return setups, nil
}

func (r *Runner) buildDirectSetups(ec evalCtx, reps int) ([]runSetup, error) {
	if !r.want("direct") {
		return nil, nil
	}
	var setups []runSetup
	for rep := range reps {
		s, err := r.rawSetup(ec, rep)
		if err != nil {
			return nil, fmt.Errorf("raw setup rep %d: %w", rep+1, err)
		}
		setups = append(setups, s)
	}
	return setups, nil
}

func (r *Runner) buildFormatSetups(ec evalCtx, i, reps int) ([]runSetup, error) {
	var setups []runSetup
	for rep := range reps {
		ss, err := r.buildRepSetups(ec, i, rep)
		if err != nil {
			return nil, err
		}
		setups = append(setups, ss...)
	}
	return setups, nil
}

type setupCandidate struct {
	label string
	fn    func() (runSetup, error)
}

func (r *Runner) buildRepSetups(ec evalCtx, i, rep int) ([]runSetup, error) {
	var setups []runSetup
	for _, c := range r.repCandidates(ec, i, rep) {
		s, err := r.buildRepSetup(c.label, rep, c.fn)
		if err != nil {
			return nil, err
		}
		if s.cmd != nil {
			setups = append(setups, s)
		}
	}
	return setups, nil
}

func (r *Runner) repCandidates(ec evalCtx, i, rep int) []setupCandidate {
	return []setupCandidate{
		{"mcp-" + fmtLabel[i], func() (runSetup, error) { return r.mcpSetup(ec, i, rep) }},
		{"cli-" + fmtLabel[i], func() (runSetup, error) { return r.cliSetup(ec, i, rep) }},
		{"proxy-" + fmtLabel[i], func() (runSetup, error) { return r.proxySetup(ec, i, rep) }},
	}
}

func (r *Runner) buildRepSetup(label string, rep int, build func() (runSetup, error)) (runSetup, error) {
	if !r.want(label) {
		return runSetup{}, nil
	}
	s, err := build()
	if err != nil {
		return runSetup{}, fmt.Errorf("%s setup rep %d: %w", label, rep+1, err)
	}
	return s, nil
}

func (r *Runner) proxySetup(ec evalCtx, i, rep int) (runSetup, error) {
	env, p := ec.Env, ec.Params
	callDir := env.DebugDir(fmt.Sprintf("proxy-%s-%d", fmtLabel[i], rep+1))
	cfg, err := r.proxyMCPConfig(env, p.Servers, callDir, i)
	if err != nil {
		return runSetup{}, err
	}
	workDir, err := freshWorkDir(env, p.WorkSrcDir)
	if err != nil {
		return runSetup{}, err
	}
	return newMCPRunSetup(mcpRunSetupParams{"proxy", i, rep, callDir, workDir, cfg, proxyAllowedTools(p.Servers, p.AllowedTools), ec.Task}), nil
}

func (r *Runner) rawSetup(ec evalCtx, rep int) (runSetup, error) {
	env, p := ec.Env, ec.Params
	callDir := env.DebugDir(fmt.Sprintf("raw-%d", rep+1))
	cfg, err := rawMCPConfig(env, r, p.Servers, callDir)
	if err != nil {
		return runSetup{}, err
	}
	workDir, err := freshWorkDir(env, p.WorkSrcDir)
	if err != nil {
		return runSetup{}, err
	}
	return newMCPRunSetup(mcpRunSetupParams{"direct", 0, rep, callDir, workDir, cfg, rawAllowedTools(p.Servers, p.AllowedTools), ec.Task}), nil
}

func (r *Runner) mcpSetup(ec evalCtx, i, rep int) (runSetup, error) {
	env, p := ec.Env, ec.Params
	callDir := env.DebugDir(fmt.Sprintf("mcp-%s-%d", fmtLabel[i], rep+1))
	cfg, err := r.miniMCPConfig(env, p.Servers, callDir, i)
	if err != nil {
		return runSetup{}, err
	}
	workDir, err := freshWorkDir(env, p.WorkSrcDir)
	if err != nil {
		return runSetup{}, err
	}
	return newMCPRunSetup(mcpRunSetupParams{"mcp", i, rep, callDir, workDir, cfg, miniMCPAllowedTools(p.AllowedTools), ec.Task}), nil
}

func (r *Runner) cliSetup(ec evalCtx, i, rep int) (runSetup, error) {
	env, p := ec.Env, ec.Params
	callDir := env.DebugDir(fmt.Sprintf("cli-%s-%d", fmtLabel[i], rep+1))
	cfgDir, err := r.miniCLIConfigDir(env, p.Servers, callDir, i)
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
	return newCLIRunSetup(CLIRunSetupParams{Idx: i, Rep: rep, CallDir: callDir, WorkDir: workDir, WrapDir: wrapDir, Allowed: cliAllowedTools(p.AllowedTools), Task: ec.Task}), nil
}

type mcpRunSetupParams struct {
	kind                                 string
	idx, rep                             int
	callDir, workDir, cfg, allowed, task string
}

func newMCPRunSetup(p mcpRunSetupParams) runSetup {
	return runSetup{
		kind: p.kind, idx: p.idx, rep: p.rep, callDir: p.callDir, workDir: p.workDir,
		cmd: func() *exec.Cmd { return buildClaudeCmd(p.cfg, p.allowed, p.workDir, p.task) },
	}
}

type CLIRunSetupParams struct {
	Idx, Rep                                 int
	CallDir, WorkDir, WrapDir, Allowed, Task string
}

func newCLIRunSetup(p CLIRunSetupParams) runSetup {
	return runSetup{
		kind: "cli", idx: p.Idx, rep: p.Rep, callDir: p.CallDir, workDir: p.WorkDir,
		cmd: func() *exec.Cmd { return buildClaudeCLICmd(p.WrapDir, p.Allowed, p.WorkDir, p.Task) },
	}
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
		go func() { ch <- r.runOne(s) }()
	}
	return r.gatherResults(ch, len(setups))
}

func (r *Runner) runOne(s runSetup) jobResult {
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
	return jobResult{s.kind, s.idx, s.rep, result}
}

type runBuckets struct {
	direct          []ClaudeResult
	mcp, cli, proxy [numFormats][]ClaudeResult
}

func newRunBuckets(reps int) runBuckets {
	b := runBuckets{direct: make([]ClaudeResult, reps)}
	for i := range numFormats {
		b.mcp[i] = make([]ClaudeResult, reps)
		b.cli[i] = make([]ClaudeResult, reps)
		b.proxy[i] = make([]ClaudeResult, reps)
	}
	return b
}

func (b *runBuckets) assign(res jobResult) {
	switch res.kind {
	case "direct":
		b.direct[res.rep] = res.r
	case "mcp":
		b.mcp[res.idx][res.rep] = res.r
	case "cli":
		b.cli[res.idx][res.rep] = res.r
	case "proxy":
		b.proxy[res.idx][res.rep] = res.r
	}
}

func (r *Runner) gatherResults(ch <-chan jobResult, n int) EvalResult {
	b := newRunBuckets(r.reps())
	for range n {
		b.assign(<-ch)
	}
	var eval EvalResult
	assignEvalResults(&eval, b, r.want)
	return eval
}

func assignEvalResults(eval *EvalResult, b runBuckets, want func(string) bool) {
	if want("direct") {
		eval.Direct = RunStats{Runs: b.direct}
	}
	for i := range numFormats {
		assignIndexedRunStat(&eval.MCP[i], b.mcp[i], want(modeLabel("mcp", i)))
		assignIndexedRunStat(&eval.CLI[i], b.cli[i], want(modeLabel("cli", i)))
		assignIndexedRunStat(&eval.Proxy[i], b.proxy[i], want(modeLabel("proxy", i)))
	}
}

func assignIndexedRunStat(dst *RunStats, runs []ClaudeResult, enabled bool) {
	if enabled {
		*dst = RunStats{Runs: runs}
	}
}
