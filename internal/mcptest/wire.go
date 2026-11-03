package mcptest

import (
	"encoding/json"
	"net/http"
)

const codeMethodNotFound = -32601

var defaultInputSchema = json.RawMessage(`{"type":"object"}`)

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	Tools struct{} `json:"tools"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolsListResult struct {
	Tools []Tool `json:"tools"`
}

func decodeRequest(w http.ResponseWriter, r *http.Request) (Request, bool) {
	var msg rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "mcptest: request is not a JSON-RPC message: "+err.Error(), http.StatusBadRequest)
		return Request{}, false
	}
	if string(msg.ID) == "null" {
		msg.ID = nil
	}
	return Request{Method: msg.Method, ID: msg.ID, Header: r.Header.Clone()}, true
}

func writeReply(w http.ResponseWriter, id json.RawMessage, reply Reply) {
	if reply.Status != 0 {
		w.WriteHeader(reply.Status)
		//nolint:errcheck // a failed write reaches the client as a short body, which the test sees.
		w.Write(reply.Body)
		return
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: reply.Result, Error: reply.Error}
	if resp.Result == nil && resp.Error == nil {
		resp.Result = struct{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck // a failed write surfaces as the client's read error
}

func withDefaultSchemas(tools []Tool) []Tool {
	out := make([]Tool, len(tools))
	for i, tool := range tools {
		if tool.InputSchema == nil {
			tool.InputSchema = defaultInputSchema
		}
		out[i] = tool
	}
	return out
}
