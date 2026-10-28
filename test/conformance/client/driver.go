package main

import (
	"encoding/json"
	"errors"
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
	var names []string
	err := d.withReauthorization(func() error {
		var listErr error
		names, listErr = d.mini.listTools()
		return listErr
	})
	tools := make([]transport.ToolDefinition, 0, len(names))
	for _, name := range names {
		tools = append(tools, transport.ToolDefinition{Name: name})
	}
	return tools, err
}

func (d cliDriver) callTool(call toolCall) (toolResult, error) {
	err := d.withReauthorization(func() error { return d.mini.call(call) })
	return toolResult{ResultType: transport.ResultComplete}, err
}

// withReauthorization runs `mini auth` and tries once more when an auth scenario's request is refused, as a user
// does after a scope challenge; mini doesn't retry a failed call itself.
func (d cliDriver) withReauthorization(attempt func() error) error {
	err := attempt()
	if err == nil || !d.reauthorize || !isUnauthorized(err) {
		return err
	}
	if authErr := d.mini.auth().failure("mini auth retry"); authErr != nil {
		return errors.Join(err, authErr)
	}
	return attempt()
}

func (cliDriver) close() error { return nil }
