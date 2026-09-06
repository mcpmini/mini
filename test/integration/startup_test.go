//go:build integration

package integration_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestStartup_ServesInitializeBeforeSlowUpstreamConnects(t *testing.T) {
	cfg := t.TempDir()

	healthyDir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	writeFakeServer(t, cfg, "healthy", healthyDir)

	hungDir := mockFixtureDir(t, map[string]string{"never": `{}`})
	fault := map[string]any{"method": "initialize", "type": "slow_initialize", "delay_ms": 5000}
	faultJSON, _ := json.Marshal(fault)
	writeFaultServer(t, faultServerParams{
		ConfigDir: cfg, ServerName: "hung", Fixtures: hungDir, FaultJSON: string(faultJSON),
		Extra: "connect_timeout: \"1s\"\n",
	})

	stdin, scanner, stderr := startMiniCmdCapturingStderr(t, cfg)
	c := newMCPClient(t, stdin, scanner)

	start := time.Now()
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	elapsed := time.Since(start)
	t.Logf("initialize responded in %v (hung upstream slow_initialize delay=5s, connect_timeout=1s)", elapsed)
	if elapsed > time.Second {
		t.Fatalf("initialize took %v; want a fast response despite a 5s-slow-init upstream", elapsed)
	}

	settleUntil(t, func() string { return c.listTools("") },
		func(s string) bool { return strings.Contains(s, "get_item") })

	healthyTools := c.listTools("healthy")
	if !strings.Contains(healthyTools, "get_item") {
		t.Errorf("expected healthy server's tools callable, got: %q", healthyTools)
	}

	if _, isErr := c.execToolAllowError("hung", "never", nil); !isErr {
		t.Error("expected hung upstream's tool call to fail: it never connects within its 1s connect_timeout")
	}

	waitForStderrContains(t, stderr, "upstream unavailable at startup")
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startMiniCmdCapturingStderr(t *testing.T, configDir string) (stdin io.WriteCloser, scanner *bufio.Scanner, stderr *syncBuffer) {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "connect", "--standalone", "--tool-mode", "compact", "--log-level", "warn")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	errBuf := &syncBuffer{}
	cmd.Stderr = errBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 4<<20), 4<<20)
	return stdin, sc, errBuf
}

func waitForStderrContains(t *testing.T, stderr *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stderr.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stderr did not contain %q within deadline; got: %s", want, stderr.String())
}

// standaloneMini owns a running mini process and its stdio pipes.
// done is closed (not sent to) when cmd.Wait() returns, so multiple
// receivers (exitCode and t.Cleanup) can wait concurrently without racing.
type standaloneMini struct {
	Cmd   *exec.Cmd
	Stdin io.WriteCloser
	Out   *bufio.Scanner
	done  chan struct{}
	code  int // set before done is closed; safe to read after receiving from done
}

func (p *standaloneMini) exitCode(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case <-p.done:
		return p.code
	case <-time.After(timeout):
		t.Fatalf("process did not exit within %v", timeout)
		return -1
	}
}

func waitExitCode(p *standaloneMini) {
	go func() {
		defer close(p.done)
		if err := p.Cmd.Wait(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				p.code = exit.ExitCode()
			} else {
				p.code = -1
			}
		}
	}()
}

func startMiniForSignal(t *testing.T, configDir string) *standaloneMini {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "connect", "--standalone", "--log-level", "error")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &standaloneMini{Cmd: cmd, Stdin: stdin, done: make(chan struct{})}
	waitExitCode(p)
	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill() //nolint:errcheck
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
		}
	})
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 4<<20), 4<<20)
	p.Out = sc
	return p
}

func doMiniHandshake(t *testing.T, p *standaloneMini) {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	}
	b, _ := json.Marshal(req)
	fmt.Fprintf(p.Stdin, "%s\n", b) //nolint:errcheck
	if !p.Out.Scan() {
		t.Fatal("no initialize response from mini")
	}
	notif := map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}}
	b, _ = json.Marshal(notif)
	fmt.Fprintf(p.Stdin, "%s\n", b) //nolint:errcheck
}

func TestStandaloneSIGTERM_openStdinExitsSuccessfully(t *testing.T) {
	proc := startMiniForSignal(t, t.TempDir())
	doMiniHandshake(t, proc)
	proc.Cmd.Process.Signal(syscall.SIGTERM) //nolint:errcheck
	if code := proc.exitCode(t, 2*time.Second); code != 0 {
		t.Fatalf("expected exit code 0 after SIGTERM with open stdin, got %d", code)
	}
}

func TestStandaloneSIGINT_openStdinExitsSuccessfully(t *testing.T) {
	proc := startMiniForSignal(t, t.TempDir())
	doMiniHandshake(t, proc)
	proc.Cmd.Process.Signal(syscall.SIGINT) //nolint:errcheck
	if code := proc.exitCode(t, 2*time.Second); code != 0 {
		t.Fatalf("expected exit code 0 after SIGINT with open stdin, got %d", code)
	}
}

func TestStandaloneSIGTERM_delayedConnectIsDrained(t *testing.T) {
	cfg := t.TempDir()
	hungDir := mockFixtureDir(t, map[string]string{"never": `{}`})
	fault := map[string]any{"method": "initialize", "type": "slow_initialize", "delay_ms": 30000}
	faultJSON, _ := json.Marshal(fault)
	writeFaultServer(t, faultServerParams{
		ConfigDir: cfg, ServerName: "hung", Fixtures: hungDir,
		FaultJSON: string(faultJSON), Extra: "connect_timeout: \"10s\"\n",
	})
	proc := startMiniForSignal(t, cfg)
	doMiniHandshake(t, proc)
	proc.Cmd.Process.Signal(syscall.SIGTERM) //nolint:errcheck
	if code := proc.exitCode(t, 2*time.Second); code != 0 {
		t.Fatalf("expected exit code 0 after SIGTERM with hung upstream, got %d", code)
	}
}
