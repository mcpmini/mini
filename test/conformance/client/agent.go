package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

const (
	requestTimeout  = 15 * time.Second
	toolCallTimeout = 30 * time.Second
	readyTimeout    = 15 * time.Second
	readyPoll       = 250 * time.Millisecond
	closeTimeout    = 5 * time.Second
)

type agentDriver struct {
	command *exec.Cmd
	input   io.WriteCloser
	replies chan agentReply
	lastID  atomic.Int64
	cancel  context.CancelFunc
}

type agentReply struct {
	response transport.Response
	err      error
}

type metaOnlyParams struct {
	Meta *transport.RequestMeta `json:"_meta"`
}

func openAgent(mini miniCLI) (*agentDriver, error) {
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, mini.bin, "--config", mini.configDir, "connect", "--standalone")
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		cancel()
		return nil, err
	}
	d := &agentDriver{command: command, input: input, replies: make(chan agentReply, 16), cancel: cancel}
	go d.readReplies(bufio.NewReader(stdout))
	if err := d.handshake(); err != nil {
		return nil, errors.Join(err, d.close())
	}
	return d, nil
}

func (d *agentDriver) handshake() error {
	if err := d.request(
		rpcCall{Method: "server/discover", Params: metaOnlyParams{Meta: agentMeta()}, Timeout: requestTimeout},
		nil,
	); err != nil {
		return err
	}
	return d.awaitUpstream()
}

func (d *agentDriver) close() error {
	closeErr := d.input.Close()
	wait := make(chan error, 1)
	go func() { wait <- d.command.Wait() }()
	var waitErr error
	select {
	case waitErr = <-wait:
	case <-time.After(closeTimeout):
		d.cancel()
		waitErr = <-wait
	}
	return errors.Join(closeErr, waitErr)
}

func (d *agentDriver) tools() ([]transport.ToolDefinition, error) {
	var listed transport.ToolsListResult
	if err := d.request(
		rpcCall{Method: "tools/list", Params: metaOnlyParams{Meta: agentMeta()}, Timeout: requestTimeout},
		&listed,
	); err != nil {
		return nil, err
	}
	var tools []transport.ToolDefinition
	for _, tool := range listed.Tools {
		if name, ok := strings.CutPrefix(tool.Name, serverName+"__"); ok {
			tool.Name = name
			tools = append(tools, tool)
		}
	}
	return tools, nil
}

func (d *agentDriver) callTool(call toolCall) (toolResult, error) {
	proxied := call
	proxied.Name = serverName + "__" + call.Name
	var result toolResult
	if err := d.request(
		rpcCall{Method: "tools/call", Params: agentToolCall(proxied), Timeout: toolCallTimeout},
		&result,
	); err != nil {
		return toolResult{}, fmt.Errorf("%s: %w", call.Name, err)
	}

	if result.IsError {
		return toolResult{}, fmt.Errorf("%s: tool error: %s", call.Name, contentText(result.Content))
	}
	return result, nil
}

func agentToolCall(call toolCall) transport.ToolCallParams {
	params := transport.NewToolCallParams(call.Name, call.Arguments, call.Round)
	params.Meta = agentMeta()
	return params
}

func agentMeta() *transport.RequestMeta {
	return &transport.RequestMeta{
		ProtocolVersion:    transport.ModernVersions[0],
		ClientInfo:         &transport.ClientInfo{Name: "spec-harness", Version: "0.1"},
		ClientCapabilities: &transport.ClientCapabilities{Elicitation: json.RawMessage(`{"form":{}}`)},
	}
}

func (d *agentDriver) awaitUpstream() error {
	status := agentToolCall(toolCall{Name: "config", Arguments: map[string]any{"action": "status"}})
	for deadline := time.Now().Add(readyTimeout); time.Now().Before(deadline); time.Sleep(readyPoll) {
		var result toolResult
		if err := d.request(
			rpcCall{Method: "tools/call", Params: status, Timeout: requestTimeout},
			&result,
		); err != nil {
			return err
		}
		if upstreamReady(result) {
			return nil
		}
	}
	return fmt.Errorf("server %s not ready after %s", serverName, readyTimeout)
}

func upstreamReady(status toolResult) bool {
	var report struct {
		Servers map[string]json.RawMessage `json:"servers"`
	}
	if json.Unmarshal([]byte(contentText(status.Content)), &report) != nil {
		return false
	}
	_, ready := report.Servers[serverName]
	return ready
}

func contentText(content []transport.ContentItem) string {
	var text strings.Builder
	for _, item := range content {
		text.WriteString(item.Text)
	}
	return text.String()
}

type rpcCall struct {
	Method  string
	Params  any
	Timeout time.Duration
}

func (d *agentDriver) request(call rpcCall, result any) error {
	id, err := d.send(call)
	if err != nil {
		return err
	}
	response, err := d.waitResponse(id, call.Timeout)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("protocol error %d: %s", response.Error.Code, response.Error.Message)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(response.Result, result)
}

func (d *agentDriver) send(call rpcCall) (int64, error) {
	id := d.lastID.Add(1)
	params, err := json.Marshal(call.Params)
	if err != nil {
		return 0, err
	}
	message, err := json.Marshal(
		transport.Request{JsonRPC: transport.JsonRPCVersion, ID: id, Method: call.Method, Params: params},
	)
	if err != nil {
		return 0, err
	}
	_, err = fmt.Fprintln(d.input, string(message))
	return id, err
}

func (d *agentDriver) waitResponse(id int64, timeout time.Duration) (transport.Response, error) {
	deadline := time.After(timeout)
	for {
		select {
		case reply, ok := <-d.replies:
			if !ok {
				return transport.Response{}, io.EOF
			}
			if reply.err != nil {
				return transport.Response{}, reply.err
			}
			if reply.response.ID == float64(id) {
				return reply.response, nil
			}
		case <-deadline:
			return transport.Response{}, errors.New("agent response timed out")
		}
	}
}

func (d *agentDriver) readReplies(output *bufio.Reader) {
	defer close(d.replies)
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			return
		}
		var response transport.Response
		if err := json.Unmarshal(line, &response); err != nil {
			d.replies <- agentReply{err: err}
			return
		}
		if response.Result != nil || response.Error != nil {
			d.replies <- agentReply{response: response}
		}
	}
}
