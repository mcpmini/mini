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
	return buildRunner(buildRunnerParams{root: root, bins: bins, modes: modes, reps: reps}), nil
}

type evalBins struct {
	MiniBin    string
	FakemcpBin string
}

type buildRunnerParams struct {
	root  string
	bins  evalBins
	modes map[string]bool
	reps  int
}

func buildRunner(p buildRunnerParams) *Runner {
	return &Runner{
		MiniBin:     p.bins.MiniBin,
		FakemcpBin:  p.bins.FakemcpBin,
		FixturesDir: filepath.Join(p.root, "benchmarks", "fixtures"),
		RepoRoot:    p.root,
		Modes:       p.modes,
		Reps:        p.reps,
	}
}

func buildEvalBins(root string) (evalBins, error) {
	miniBin, err := buildBin(buildBinParams{root: root, name: "mini", pkg: "./cmd/mini"})
	if err != nil {
		return evalBins{}, fmt.Errorf("build mini: %w", err)
	}
	fakemcpBin, err := buildBin(buildBinParams{
		root: root, name: "fakemcp", pkg: "./test/fakemcp", extraFlags: []string{"-tags", "integration"},
	})
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

type buildBinParams struct {
	root       string
	name       string
	pkg        string
	extraFlags []string
}

func buildBin(p buildBinParams) (string, error) {
	tmp, err := os.MkdirTemp("", "mini-evals-*")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, p.name)
	args := append([]string{"build"}, p.extraFlags...)
	args = append(args, "-o", out, p.pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = p.root
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

func modeLabel(kind string, idx format) string {
	if kind == "direct" {
		return "direct"
	}
	return kind + "-" + fmtLabel[idx]
}

type runSetup struct {
	kind, callDir, workDir string
	idx                    format
	rep                    int
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
		ss, err := r.buildFormatSetups(ec, format(i), reps)
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

func (r *Runner) buildFormatSetups(ec evalCtx, i format, reps int) ([]runSetup, error) {
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

func (r *Runner) buildRepSetups(ec evalCtx, i format, rep int) ([]runSetup, error) {
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

func (r *Runner) repCandidates(ec evalCtx, i format, rep int) []setupCandidate {
	return []setupCandidate{
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

func (r *Runner) proxySetup(ec evalCtx, i format, rep int) (runSetup, error) {
	env, p := ec.Env, ec.Params
	callDir := env.DebugDir(fmt.Sprintf("proxy-%s-%d", fmtLabel[i], rep+1))
	cfg, err := r.proxyMCPConfig(miniConfigParams{env: env, servers: p.Servers, callLogDir: callDir, format: i})
	if err != nil {
		return runSetup{}, err
	}
	workDir, err := freshWorkDir(env, p.WorkSrcDir)
	if err != nil {
		return runSetup{}, err
	}
	return newMCPRunSetup(
		mcpRunSetupParams{
			"proxy",
			i,
			rep,
			callDir,
			workDir,
			cfg,
			proxyAllowedTools(p.Servers, p.AllowedTools),
			ec.Task,
		},
	), nil
}

func (r *Runner) rawSetup(ec evalCtx, rep int) (runSetup, error) {
	env, p := ec.Env, ec.Params
	callDir := env.DebugDir(fmt.Sprintf("raw-%d", rep+1))
	cfg, err := r.rawMCPConfig(env, p.Servers, callDir)
	if err != nil {
		return runSetup{}, err
	}
	workDir, err := freshWorkDir(env, p.WorkSrcDir)
	if err != nil {
		return runSetup{}, err
	}
	return newMCPRunSetup(
		mcpRunSetupParams{"direct", 0, rep, callDir, workDir, cfg, rawAllowedTools(p.Servers, p.AllowedTools), ec.Task},
	), nil
}

func (r *Runner) cliSetup(ec evalCtx, i format, rep int) (runSetup, error) {
	env, p := ec.Env, ec.Params
	callDir := env.DebugDir(fmt.Sprintf("cli-%s-%d", fmtLabel[i], rep+1))
	cfgDir, err := r.miniCLIConfigDir(miniConfigParams{env: env, servers: p.Servers, callLogDir: callDir, format: i})
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
	return newCLIRunSetup(
		CLIRunSetupParams{
			Idx:     i,
			Rep:     rep,
			CallDir: callDir,
			WorkDir: workDir,
			WrapDir: wrapDir,
			Allowed: cliAllowedTools(p.AllowedTools),
			Task:    ec.Task,
		},
	), nil
}

type mcpRunSetupParams struct {
	kind                                 string
	idx                                  format
	rep                                  int
	callDir, workDir, cfg, allowed, task string
}

func newMCPRunSetup(p mcpRunSetupParams) runSetup {
	return runSetup{
		kind: p.kind, idx: p.idx, rep: p.rep, callDir: p.callDir, workDir: p.workDir,
		cmd: func() *exec.Cmd {
			return buildClaudeCmd(claudeMCPCommandParams{
				ConfigFile: p.cfg, AllowedTools: p.allowed, WorkDir: p.workDir, Task: p.task,
			})
		},
	}
}

type CLIRunSetupParams struct {
	Idx                                      format
	Rep                                      int
	CallDir, WorkDir, WrapDir, Allowed, Task string
}

func newCLIRunSetup(p CLIRunSetupParams) runSetup {
	return runSetup{
		kind: "cli", idx: p.Idx, rep: p.Rep, callDir: p.CallDir, workDir: p.WorkDir,
		cmd: func() *exec.Cmd {
			return buildClaudeCLICmd(claudeCLICommandParams{
				WrapperDir: p.WrapDir, AllowedTools: p.Allowed, WorkDir: p.WorkDir, Task: p.Task,
			})
		},
	}
}

type jobResult struct {
	kind string
	idx  format
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
	direct     []ClaudeResult
	cli, proxy [numFormats][]ClaudeResult
}

func newRunBuckets(reps int) runBuckets {
	b := runBuckets{direct: make([]ClaudeResult, reps)}
	for i := range numFormats {
		b.cli[i] = make([]ClaudeResult, reps)
		b.proxy[i] = make([]ClaudeResult, reps)
	}
	return b
}

func (b *runBuckets) assign(res jobResult) {
	switch res.kind {
	case "direct":
		b.direct[res.rep] = res.r
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
		assignIndexedRunStat(&eval.CLI[i], b.cli[i], want(modeLabel("cli", format(i))))
		assignIndexedRunStat(&eval.Proxy[i], b.proxy[i], want(modeLabel("proxy", format(i))))
	}
}

func assignIndexedRunStat(dst *RunStats, runs []ClaudeResult, enabled bool) {
	if enabled {
		*dst = RunStats{Runs: runs}
	}
}
