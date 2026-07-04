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

func runDeno(runCtx context.Context, denoPath, program string) (runResult, error) {
	cmd := newDenoCmd(runCtx, denoPath, program)

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

func newDenoCmd(runCtx context.Context, denoPath, program string) *exec.Cmd {
	cmd := exec.CommandContext(runCtx, denoPath, "run", "--no-prompt", "--no-remote", "-")
	cmd.Env = childEnv()
	cmd.Stdin = strings.NewReader(program)
	cmd.WaitDelay = 2 * time.Second
	return cmd
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
	env := []string{"DENO_NO_UPDATE_CHECK=1"}
	if v, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+v)
	}
	if v, ok := os.LookupEnv("HOME"); ok {
		env = append(env, "HOME="+v)
	}
	return env
}
