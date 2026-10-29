package main

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/transport"
)

type mode string

const (
	agentMode mode = "agent"
	cliMode   mode = "cli"
)

func parseMode(raw string) (mode, error) {
	switch m := mode(raw); m {
	case "":
		return agentMode, nil
	case agentMode, cliMode:
		return m, nil
	default:
		return "", fmt.Errorf("unsupported MINI_CONFORMANCE_MODE %q", raw)
	}
}

type driver interface {
	tools() ([]transport.ToolDefinition, error)
	callTool(call toolCall) (toolResult, error)
	close() error
}

type toolCall struct {
	Name      string                  `json:"name"`
	Arguments map[string]any          `json:"arguments"`
	Round     *transport.ToolCallMRTR `json:"-"`
}

type toolResult struct {
	ResultType    transport.ResultType    `json:"resultType"`
	IsError       bool                    `json:"isError"`
	Content       []transport.ContentItem `json:"content"`
	InputRequests map[string]inputRequest `json:"inputRequests"`
	RequestState  json.RawMessage         `json:"requestState"`
}

func openDriver(sc scenario, mini miniCLI) (driver, error) {
	if sc.Mode == cliMode {
		return cliDriver{mini: mini, reauthorize: sc.isAuth()}, nil
	}
	return openAgent(mini)
}

type cliDriver struct {
	mini        miniCLI
	reauthorize bool
}

func (d cliDriver) tools() ([]transport.ToolDefinition, error) {
	names, err := d.mini.listTools()
	if err != nil && d.reauthorize && isUnauthorized(err) {
		logLoginFailure("mini auth retry", d.mini.auth())
		names, err = d.mini.listTools()
	}
	tools := make([]transport.ToolDefinition, 0, len(names))
	for _, name := range names {
		tools = append(tools, transport.ToolDefinition{Name: name})
	}
	return tools, err
}

func (d cliDriver) callTool(call toolCall) (toolResult, error) {
	return toolResult{ResultType: transport.ResultComplete}, d.mini.call(call)
}

func (cliDriver) close() error { return nil }
