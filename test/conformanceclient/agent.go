package main

import (
	"bufio"
	"context"
	"encoding/json"
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
	modernVersion = "2026-07-28"
	versionMeta   = "io.modelcontextprotocol/protocolVersion"
	capsMeta      = "io.modelcontextprotocol/clientCapabilities"
	infoMeta      = "io.modelcontextprotocol/clientInfo"
)

type agentPeer struct {
	command *exec.Cmd
	input   io.WriteCloser
	replies chan agentReply
	request atomic.Int64
	cancel  context.CancelFunc
}

type agentReply struct {
	response transport.Response
	err      error
}

type agentStartParams struct {
	Binary string
	Config string
}

func startAgent(params agentStartParams) (*agentPeer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, params.Binary, "--config", params.Config, "connect", "--standalone")
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
	peer := &agentPeer{command: command, input: input, replies: make(chan agentReply, 16), cancel: cancel}
	go peer.readReplies(bufio.NewReader(stdout))
	return peer, nil
}

func (peer *agentPeer) close() error {
	closeErr := peer.input.Close()
	wait := make(chan error, 1)
	go func() { wait <- peer.command.Wait() }()
	var waitErr error
	select {
	case waitErr = <-wait:
	case <-time.After(5 * time.Second):
		peer.cancel()
		waitErr = <-wait
	}
	if closeErr != nil {
		return closeErr
	}
	return waitErr
}

func (peer *agentPeer) call(method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	id := peer.request.Add(1)
	message, err := makeAgentRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if _, writeErr := fmt.Fprintln(peer.input, string(encoded)); writeErr != nil {
		return nil, writeErr
	}
	answer, err := peer.waitResponse(id, timeout)
	if err != nil {
		return nil, err
	}
	return decodeAgentResponse(answer)
}

func makeAgentRequest(id int64, method string, params map[string]any) (transport.Request, error) {
	encodedParams, err := marshalAgentParams(params)
	if err != nil {
		return transport.Request{}, err
	}
	return transport.Request{
		JsonRPC: transport.JsonRPCVersion, ID: id, Method: method, Params: encodedParams,
	}, nil
}

func (peer *agentPeer) waitResponse(id int64, timeout time.Duration) (transport.Response, error) {
	deadline := time.After(timeout)
	for {
		select {
		case answer, ok := <-peer.replies:
			if !ok {
				return transport.Response{}, io.EOF
			}
			if answer.err != nil {
				return transport.Response{}, answer.err
			}
			if answer.response.ID == float64(id) {
				return answer.response, nil
			}
		case <-deadline:
			return transport.Response{}, fmt.Errorf("agent response timed out")
		}
	}
}

func (peer *agentPeer) readReplies(output *bufio.Reader) {
	defer close(peer.replies)
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			return
		}
		var response transport.Response
		if err := json.Unmarshal(line, &response); err != nil {
			peer.replies <- agentReply{err: err}
			return
		}
		if response.Result != nil || response.Error != nil {
			peer.replies <- agentReply{response: response}
		}
	}
}

func decodeAgentResponse(response transport.Response) (map[string]any, error) {
	if response.Error != nil {
		return nil, fmt.Errorf("protocol error %d: %s", response.Error.Code, response.Error.Message)
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Result, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func marshalAgentParams(params map[string]any) (json.RawMessage, error) {
	meta := map[string]any{
		versionMeta: modernVersion,
		capsMeta:    map[string]any{"elicitation": map[string]any{"form": map[string]any{}}},
		infoMeta:    map[string]any{"name": "spec-harness", "version": "0.1"},
	}
	fields := make(map[string]any, len(params)+1)
	for key, value := range params {
		fields[key] = value
	}
	fields["_meta"] = meta
	encoded, err := json.Marshal(fields)
	return encoded, err
}

func (peer *agentPeer) tools() ([]tool, error) {
	if _, err := peer.call("server/discover", map[string]any{}, 15*time.Second); err != nil {
		return nil, err
	}
	return peer.pollTools()
}

func (peer *agentPeer) pollTools() ([]tool, error) {
	if err := peer.awaitUpstream(); err != nil {
		return nil, err
	}
	result, err := peer.call("tools/list", map[string]any{}, 15*time.Second)
	if err != nil {
		return nil, err
	}
	tools, err := decodeTools(result)
	if err != nil {
		return nil, err
	}
	return matchingTools(tools), nil
}

func (peer *agentPeer) awaitUpstream() error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		result, err := peer.call("tools/call", map[string]any{
			"name": "config", "arguments": map[string]any{"action": "status"},
		}, 15*time.Second)
		if err != nil {
			return err
		}
		if upstreamReady(result) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("server conf not ready after 15 seconds")
}

func upstreamReady(statusResult map[string]any) bool {
	content, _ := statusResult["content"].([]any)
	if len(content) == 0 {
		return false
	}
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	var status struct {
		Servers map[string]json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		return false
	}
	_, ready := status.Servers["conf"]
	return ready
}

func matchingTools(tools []tool) []tool {
	var matching []tool
	for _, item := range tools {
		name, ok := strings.CutPrefix(item.Name, "conf__")
		if !ok {
			continue
		}
		item.Name = name
		matching = append(matching, item)
	}
	return matching
}

func decodeTools(value map[string]any) ([]tool, error) {
	encoded, err := json.Marshal(value["tools"])
	if err != nil {
		return nil, err
	}
	var tools []tool
	if err := json.Unmarshal(encoded, &tools); err != nil {
		return nil, err
	}
	return tools, nil
}

func (peer *agentPeer) callTool(name string, args, extra map[string]any) (map[string]any, error) {
	params := map[string]any{"name": "conf__" + name, "arguments": args}
	for key, value := range extra {
		params[key] = value
	}
	result, err := peer.call("tools/call", params, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return completedToolResult(name, result)
}

func completedToolResult(name string, result map[string]any) (map[string]any, error) {
	if result["isError"] == true {
		return nil, fmt.Errorf("%s: tool error %v", name, result["content"])
	}
	return result, nil
}
