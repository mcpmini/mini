package transport

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

type StdioConnection struct {
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	scanner       *bufio.Scanner
	pending       pendingMap
	nextID        atomic.Int64
	done          chan struct{}
	closeOnce     sync.Once // guards close(done)
	shutdownOnce  sync.Once // guards stdin.Close, Kill, Wait
	waitErr       error     // set once by shutdownOnce
	logger        *slog.Logger
	command       StdioCommand
	clock         clock.Clock
	modernVersion atomic.Pointer[string]
	writeGate     chan struct{} // not a mutex, so a cancelled writer can stop waiting
	listenMu      sync.Mutex
	listen        *stdioSubscription
	listenCancel  context.CancelFunc
	listenDone    chan struct{}
	toolsChanged  toolsChangedNotifier
}

type StdioCommand struct {
	Command string
	Args    []string
	Env     []string
	Logger  *slog.Logger
	Clock   clock.Clock
}

func NewStdioConnection(ctx context.Context, p StdioCommand) (*StdioConnection, error) {
	c, err := startSubprocess(context.WithoutCancel(ctx), p)
	if err != nil {
		return nil, err
	}
	c, err = c.negotiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("MCP connection setup: %w", err)
	}
	return c, nil
}

// The server's own stderr is drained within milliseconds of the kill; only a child that outlives
// it keeps the pipe open longer, and what it writes once the server is gone doesn't matter, so the
// delay only bounds how long closing stalls.
const stdioWaitDelay = 500 * time.Millisecond

func startSubprocess(ctx context.Context, p StdioCommand) (*StdioConnection, error) {
	cmd := exec.CommandContext(ctx, p.Command, p.Args...)
	if len(p.Env) > 0 {
		cmd.Env = append(cmd.Environ(), p.Env...)
	}
	stdin, stdout, err := cmdPipes(cmd)
	if err != nil {
		return nil, err
	}
	cmd.Stderr = &prefixWriter{logger: p.Logger, prefix: "[" + p.Command + "] "}
	// Kill reaches only the server's own process. A child it started (sh -c, npx) can hold stderr
	// open, and Wait would block until that child exits too.
	cmd.WaitDelay = stdioWaitDelay
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", p.Command, err)
	}
	c := newConn(newStdioConnParams{cmd: cmd, stdin: stdin, stdout: stdout, logger: p.Logger})
	c.command = p
	if p.Clock != nil {
		c.clock = p.Clock
	}
	go c.readLoop()
	return c, nil
}

func cmdPipes(cmd *exec.Cmd) (io.WriteCloser, io.ReadCloser, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	return stdin, stdout, nil
}

type newStdioConnParams struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	logger *slog.Logger
}

func newConn(p newStdioConnParams) *StdioConnection {
	writeGate := make(chan struct{}, 1)
	writeGate <- struct{}{}
	return &StdioConnection{
		cmd:       p.cmd,
		stdin:     p.stdin,
		scanner:   NewScanner(p.stdout),
		pending:   newPendingMap(),
		done:      make(chan struct{}),
		logger:    p.logger,
		clock:     clock.System(),
		writeGate: writeGate,
	}
}

func (c *StdioConnection) Health(_ context.Context) error {
	select {
	case <-c.done:
		return fmt.Errorf("process exited")
	default:
		return nil
	}
}

func (c *StdioConnection) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *StdioConnection) Close() error {
	c.closeDone()
	if c.listenCancel != nil {
		c.listenCancel()
	}
	waitErr := c.shutdownProcess()
	if c.listenDone != nil {
		<-c.listenDone
	}
	return waitErr
}

func (c *StdioConnection) shutdownProcess() error {
	c.shutdownOnce.Do(func() {
		c.stdin.Close() //nolint:errcheck // child termination and Wait below own subprocess cleanup
		if c.cmd.Process != nil {
			//nolint:errcheck // an exited child may reject Kill; Wait observes its exit status.
			c.cmd.Process.Kill()
		}
		c.waitErr = c.cmd.Wait()
	})
	return c.waitErr
}

// closeDone signals the done channel once, unblocking readLoop and any pending Call.
func (c *StdioConnection) closeDone() {
	c.closeOnce.Do(func() { close(c.done) })
}

func (c *StdioConnection) abandon() {
	c.closeDone()
	_ = c.shutdownProcess() //nolint:errcheck // the caller reports why the connection ended
}

func exitStatus(waitErr error) string {
	if waitErr == nil {
		return "exit status 0"
	}
	return waitErr.Error()
}
