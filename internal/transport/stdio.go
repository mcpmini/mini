package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/version"
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

func (c *StdioConnection) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if modernVersion := c.negotiatedModernVersion(); modernVersion != "" {
		prepared, err := prepareModernParams(params, modernVersion)
		if err != nil {
			return nil, err
		}
		params = prepared
	} else if method == "tools/call" {
		params = stripModernToolCallFields(params)
	}
	return c.rpc(ctx, method, params)
}

func (c *StdioConnection) rpc(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := c.pending.register(id)
	request := Request{JsonRPC: JsonRPCVersion, ID: id, Method: method, Params: params}
	if err := c.writeJSONContext(ctx, request); err != nil {
		c.pending.remove(id)
		c.abandonAfterFailedWrite(ctx, err)
		return nil, err
	}
	return c.awaitResponse(ctx, id, ch)
}

func (c *StdioConnection) awaitResponse(ctx context.Context, id int64, ch chan *Response) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		if c.pending.remove(id) && c.isModern() {
			c.cancelUpstreamRequest(context.WithoutCancel(ctx), id)
		}
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *StdioConnection) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	return paginateToolsList(ctx, c.callToolsPage)
}

func (c *StdioConnection) callToolsPage(ctx context.Context, cursor string) (ToolsListResult, error) {
	var params json.RawMessage
	if cursor != "" {
		//nolint:errcheck // cursor map contains only strings.
		params, _ = json.Marshal(map[string]string{"cursor": cursor})
	}
	raw, err := c.Call(ctx, "tools/list", params)
	if err != nil {
		return ToolsListResult{}, err
	}
	var r ToolsListResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return ToolsListResult{}, fmt.Errorf("parse tools/list: %w", err)
	}
	return r, nil
}

func (c *StdioConnection) Health(_ context.Context) error {
	select {
	case <-c.done:
		return fmt.Errorf("process exited")
	default:
		return nil
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

func (c *StdioConnection) initialize(ctx context.Context) error {
	//nolint:errcheck // fixed handshake fields contain only JSON-serializable values.
	params, _ := json.Marshal(newInitializeParams())
	raw, err := c.Call(ctx, "initialize", params)
	if err != nil {
		return err
	}
	result, err := parseInitializeResult(raw)
	if err != nil {
		return err
	}
	c.logger.Info("upstream connected", "server", result.ServerInfo.Name, "protocol", result.ProtocolVersion)
	return c.writeJSONContext(ctx, Notification{JsonRPC: JsonRPCVersion, Method: NotificationInitialized})
}

func newInitializeParams() InitializeParams {
	return InitializeParams{
		ProtocolVersion: LatestLegacyVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mini", Version: version.Version},
	}
}

func parseInitializeResult(raw json.RawMessage) (InitializeResult, error) {
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return InitializeResult{}, fmt.Errorf("parse initialize: %w", err)
	}
	if !IsLegacyVersion(result.ProtocolVersion) {
		return InitializeResult{}, fmt.Errorf(
			"upstream negotiated unsupported protocol version %q",
			result.ProtocolVersion,
		)
	}
	return result, nil
}

func (c *StdioConnection) readLoop() {
	defer c.closeDone()
	for c.scanner.Scan() {
		c.dispatch(c.scanner.Bytes())
	}
	if err := c.scanner.Err(); err != nil {
		c.logger.Error("upstream read error", "err", err)
	}
}

func (c *StdioConnection) dispatch(line []byte) {
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		c.logger.Error("bad upstream response", "err", err)
		return
	}
	if resp.ID == nil {
		c.dispatchNotification(line)
		return
	}
	c.noteSubscriptionResponse(&resp)
	c.pending.deliver(resp.ID, &resp)
}

func (c *StdioConnection) dispatchNotification(line []byte) {
	var notification Notification
	if err := json.Unmarshal(line, &notification); err != nil || notification.Method == "" {
		return
	}
	if c.isModern() && c.dispatchModernSubscriptionNotification(notification) {
		return
	}
	if notification.Method == NotificationToolsChanged {
		c.toolsChanged.NotifyToolsChanged()
		return
	}
	if handler := c.toolsChanged.Handler(); handler != nil {
		handler(notification)
	}
}

func (c *StdioConnection) SetNotificationHandler(handler func(Notification)) {
	c.toolsChanged.SetHandler(handler)
}

// A best-effort notice for a caller that has already given up, so it gets only a short bound.
const stdioCancelWriteTimeout = 250 * time.Millisecond

func (c *StdioConnection) cancelUpstreamRequest(parent context.Context, id int64) {
	ctx, cancel := clock.WithTimeout(parent, c.clock, stdioCancelWriteTimeout)
	defer cancel()
	params, err := json.Marshal(CancelledParams{RequestID: json.RawMessage(strconv.FormatInt(id, 10))})
	if err != nil {
		c.logger.Debug("encode upstream cancellation", "err", err)
		return
	}
	notification := Notification{JsonRPC: JsonRPCVersion, Method: NotificationCancelled, Params: params}
	if err := c.writeJSONContext(ctx, notification); err != nil {
		c.logger.Debug("send upstream cancellation", "err", err)
		c.abandonAfterFailedWrite(ctx, err)
	}
}
