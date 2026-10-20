//go:build test

package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/mcpmini/mini/internal/transport"
)

// daemonRequest is the part of a forwarded request the fake daemons route on.
type daemonRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		ToolMode string `json:"_mini_tool_mode"`
	} `json:"params"`
}

func readDaemonRequest(req *http.Request) (daemonRequest, error) {
	var request daemonRequest
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return request, err
	}
	return request, json.Unmarshal(body, &request)
}

func daemonResult(id json.RawMessage, result any) (*http.Response, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return daemonResponse(transport.Response{JsonRPC: transport.JsonRPCVersion, ID: id, Result: encoded})
}

func daemonResponse(response transport.Response) (*http.Response, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return localDaemonResponse(http.StatusOK, string(encoded)), nil
}

func localDaemonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
