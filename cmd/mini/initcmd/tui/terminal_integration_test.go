//go:build integration && !windows

package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/mcpmini/mini/internal/testutil"
)

const termWidth, termHeight = 100, 30

type terminal struct {
	t      *testing.T
	pty    *os.File
	cmd    *exec.Cmd
	mu     sync.Mutex
	screen *vt.Emulator
	exited chan struct{}
	err    error
}

type terminalParams struct {
	home      string
	configDir string
	args      []string
}

func startTerminal(t *testing.T, p terminalParams) *terminal {
	t.Helper()
	cmd := exec.Command(testutil.Binary(t, "MINIMCP_BIN"), append([]string{"--config", p.configDir}, p.args...)...)
	cmd.Env = []string{
		"HOME=" + p.home, "CODEX_HOME=", "XDG_CONFIG_HOME=" + filepath.Join(p.home, ".config"),
		"TERM=xterm-256color", "NO_COLOR=1", "PATH=" + os.Getenv("PATH"),
		// The catalog fetch goes through Go's default proxy handling. Nothing listens on port 1, so the
		// fetch fails at once and init falls back to the built-in catalog without reaching the network.
		"HTTPS_PROXY=http://127.0.0.1:1",
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: termWidth, Rows: termHeight})
	if err != nil {
		t.Fatalf("start mini in a pty: %v", err)
	}
	term := &terminal{
		t:      t,
		pty:    f,
		cmd:    cmd,
		screen: vt.NewEmulator(termWidth, termHeight),
		exited: make(chan struct{}),
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); term.copyOutput() }()
	// The UI asks the terminal about itself; the emulator's answers go back as the terminal's.
	go func() { defer wg.Done(); _, _ = io.Copy(f, term.screen) }() //nolint:errcheck // ends when cleanup closes the answer pipe
	go func() { term.err = cmd.Wait(); close(term.exited) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck // the process may have exited already
		<-term.exited
		_ = f.Close() //nolint:errcheck // the pty is done either way
		// Closing the answer pipe ends the copy; the emulator's own Close races with its Read.
		_ = term.screen.InputPipe().(io.Closer).Close() //nolint:errcheck // a pipe close can't fail
		wg.Wait()
	})
	return term
}

func (term *terminal) copyOutput() {
	buf := make([]byte, 4096)
	for {
		n, err := term.pty.Read(buf)
		term.mu.Lock()
		_, _ = term.screen.Write(buf[:n]) //nolint:errcheck // the emulator only fails once closed
		term.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (term *terminal) text() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.screen.String()
}

func (term *terminal) altScreen() bool {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.screen.IsAltScreen()
}

func (term *terminal) waitFor(want string) {
	term.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for !strings.Contains(term.text(), want) {
		if time.Now().After(deadline) {
			term.t.Fatalf("screen never showed %q:\n%s", want, term.text())
		}
		<-tick.C
	}
}

var keyBytes = map[string]string{
	"space":  " ",
	"enter":  "\r",
	"esc":    "\x1b",
	"ctrl+c": "\x03",
	"down":   "\x1b[B",
	"up":     "\x1b[A",
	"tab":    "\t",
}

func (term *terminal) press(keys ...string) {
	term.t.Helper()
	for _, key := range keys {
		raw, ok := keyBytes[key]
		if !ok {
			raw = key
		}
		if _, err := term.pty.WriteString(raw); err != nil {
			term.t.Fatalf("press %s: %v", key, err)
		}
	}
}

func (term *terminal) exitCode() int {
	term.t.Helper()
	select {
	case <-term.exited:
	case <-time.After(15 * time.Second):
		term.t.Fatalf("mini didn't exit; screen:\n%s", term.text())
	}
	var exit *exec.ExitError
	if errors.As(term.err, &exit) {
		return exit.ExitCode()
	}
	return 0
}
