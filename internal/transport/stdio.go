package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/mcpmini/mini/internal/version"
)

type StdioConnection struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	scanner      *bufio.Scanner
	pending      pendingMap
	nextID       atomic.Int64
	done         chan struct{}
	closeOnce    sync.Once // guards close(done)
	shutdownOnce sync.Once // guards stdin.Close, Kill, Wait
	logger       *slog.Logger
	mu           sync.Mutex // guards stdin writes
	toolsChanged toolsChangedNotifier
}

type StdioCommand struct {
	Command string
	Args    []string
	Env     []string
	Logger  *slog.Logger
}

func NewStdioConnection(ctx context.Context, p StdioCommand) (*StdioConnection, error) {
	c, err := startSubprocess(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("MCP handshake: %w", err)
	}
	return c, nil
}

func startSubprocess(ctx context.Context, p StdioCommand) (*StdioConnection, error) {
	cmd := exec.CommandContext(ctx, p.Command, p.Args...)
	if len(p.Env) > 0 {
		cmd.Env = p.Env
	}
	stdin, stdout, err := cmdPipes(cmd)
	if err != nil {
		return nil, err
	}
	cmd.Stderr = &prefixWriter{logger: p.Logger, prefix: "[" + p.Command + "] "}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", p.Command, err)
	}
	c := newConn(cmd, stdin, stdout, p.Logger)
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

func newConn(cmd *exec.Cmd, stdin io.WriteCloser, stdout io.ReadCloser, logger *slog.Logger) *StdioConnection {
	return &StdioConnection{
		cmd:     cmd,
		stdin:   stdin,
		scanner: NewScanner(stdout),
		pending: newPendingMap(),
		done:    make(chan struct{}),
		logger:  logger,
	}
}

func (c *StdioConnection) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := c.pending.register(id)

	if err := c.sendRequest(id, method, params); err != nil {
		c.pending.remove(id)
		return nil, err
	}

	return c.awaitResponse(ctx, id, ch)
}

func (c *StdioConnection) sendRequest(id int64, method string, params json.RawMessage) error {
	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeJSON(req)
}

func (c *StdioConnection) awaitResponse(ctx context.Context, id int64, ch chan *Response) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		c.pending.remove(id)
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
	var closeErr error
	c.shutdownOnce.Do(func() {
		c.stdin.Close()
		if c.cmd.Process != nil {
			c.cmd.Process.Kill()
		}
		closeErr = c.cmd.Wait()
	})
	return closeErr
}

// closeDone signals the done channel once, unblocking readLoop and any pending Call.
func (c *StdioConnection) closeDone() {
	c.closeOnce.Do(func() { close(c.done) })
}

func (c *StdioConnection) initialize(ctx context.Context) error {
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeJSON(Notification{JSONRPC: "2.0", Method: NotificationInitialized})
}

func newInitializeParams() InitializeParams {
	return InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mini", Version: version.Version},
	}
}

func parseInitializeResult(raw json.RawMessage) (InitializeResult, error) {
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return InitializeResult{}, fmt.Errorf("parse initialize: %w", err)
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
	c.pending.deliver(resp.ID, &resp)
}

func (c *StdioConnection) dispatchNotification(line []byte) {
	var notification Notification
	if err := json.Unmarshal(line, &notification); err != nil || notification.Method == "" {
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

func (c *StdioConnection) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.stdin, "%s\n", b)
	return err
}
