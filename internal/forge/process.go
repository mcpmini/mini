package forge

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	maxStdoutBytes = 8 << 20
	maxStderrBytes = 64 << 10
)

type runResult struct {
	stdout         []byte
	stderr         []byte
	waitErr        error
	outputTooLarge bool
}

type execOptions struct {
	packages       []string
	net            []string
	env            []string
	allowAllNet    bool
	extraEnv       []string
	bridgeHostPort string
}

func runDeno(runCtx context.Context, denoPath, program string, opts execOptions) (runResult, error) {
	cmd := newDenoCmd(runCtx, denoPath, program, opts)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return runResult{}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return runResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return runResult{}, err
	}

	result := captureOutput(cmd, stdoutPipe, stderrPipe)
	result.waitErr = cmd.Wait()
	return result, nil
}

func newDenoCmd(runCtx context.Context, denoPath, program string, opts execOptions) *exec.Cmd {
	cmd := exec.CommandContext(runCtx, denoPath, runArgs(opts)...)
	cmd.Env = append(childEnv(), opts.extraEnv...)
	cmd.Env = append(cmd.Env, grantedEnvValues(opts.env)...)
	cmd.Stdin = strings.NewReader(program)
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// runArgs must keep remoteFlags' output (--no-remote --no-npm, or
// --cached-only once packages are in play) as the only default:
// --allow-net/--allow-env are appended only when the caller has non-empty
// grants, so an unconfigured code_mode stays fully denied on both flag paths.
// --no-config prevents a deno.json discovered from the daemon's cwd (or any
// parent) from remapping imports inside the sandbox.
func runArgs(opts execOptions) []string {
	args := []string{"run", "--no-prompt", "--no-config"}
	args = append(args, remoteFlags(opts.packages)...)
	args = append(args, netFlag(opts)...)
	if len(opts.env) > 0 {
		args = append(args, "--allow-env="+strings.Join(opts.env, ","))
	}
	return append(args, "-")
}

// netFlag prefers the dangerous bare-all grant over the scoped allowlist:
// DangerousAllowAllNet ignores Net entirely rather than combining the two.
func netFlag(opts execOptions) []string {
	if opts.allowAllNet {
		return []string{"--allow-net"}
	}
	hosts := append([]string{}, opts.net...)
	if opts.bridgeHostPort != "" {
		hosts = append(hosts, opts.bridgeHostPort)
	}
	if len(hosts) == 0 {
		return nil
	}
	return []string{"--allow-net=" + strings.Join(hosts, ",")}
}

// remoteFlags: --no-remote alone does not gate npm: specifier resolution, so
// with no packages declared code could still `await import("npm:...")` and
// have Deno fetch it from the registry at runtime; --no-npm closes that hole.
// Once packages are declared, --cached-only already blocks any uncached
// package (npm or jsr) from downloading, so it's sufficient on its own.
func remoteFlags(packages []string) []string {
	if len(packages) == 0 {
		return []string{"--no-remote", "--no-npm"}
	}
	return []string{"--cached-only"}
}

// grantedEnvValues passes through values for names present in the host
// process env; a name granted but unset in the host is still allow-listed
// (via runArgs) but simply reads as undefined in the program — one unset
// var must not fail unrelated runs.
func grantedEnvValues(names []string) []string {
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			pairs = append(pairs, name+"="+v)
		}
	}
	return pairs
}

func captureOutput(cmd *exec.Cmd, stdoutPipe, stderrPipe io.Reader) runResult {
	var stdout, stderr []byte
	var tooLarge bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stdout, tooLarge = captureCapped(stdoutPipe, maxStdoutBytes)
		if tooLarge {
			killProcess(cmd)
		}
	}()
	go func() {
		defer wg.Done()
		stderr, _ = captureCapped(stderrPipe, maxStderrBytes)
		// Keep draining past the cap: once the kernel pipe buffer fills, the
		// child blocks on stderr writes and the run hangs until the timeout.
		_, _ = io.Copy(io.Discard, stderrPipe)
	}()
	wg.Wait()
	return runResult{stdout: stdout, stderr: stderr, outputTooLarge: tooLarge}
}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func captureCapped(r io.Reader, capBytes int) (data []byte, tooLarge bool) {
	data, _ = io.ReadAll(io.LimitReader(r, int64(capBytes)+1))
	if len(data) > capBytes {
		return data[:capBytes], true
	}
	return data, false
}

func childEnv() []string {
	env := []string{"DENO_NO_UPDATE_CHECK=1", "NO_COLOR=1"}
	if v, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+v)
	}
	if v, ok := os.LookupEnv("HOME"); ok {
		env = append(env, "HOME="+v)
	}
	return env
}
