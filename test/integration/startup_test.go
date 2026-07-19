//go:build integration

package integration_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"sync"
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
