package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

const (
	protocolVersionMeta = "io.modelcontextprotocol/protocolVersion"
	clientInfoMeta      = "io.modelcontextprotocol/clientInfo"
	clientCapsMeta      = "io.modelcontextprotocol/clientCapabilities"
)

type headerParameter struct {
	name string
	path []string
}

func (c *HTTPConnection) rebuildToolHeaders(tools []ToolDefinition) []ToolDefinition {
	entries := make(map[string][]headerParameter, len(tools))
	valid := make([]ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		params, reason := parseHeaderParameters(tool.InputSchema)
		if reason != "" {
			slog.Warn("dropping tool with invalid x-mcp-header",
				"server", c.serverName, "tool", tool.Name, "reason", reason)
			continue
		}
		entries[tool.Name] = params
		valid = append(valid, tool)
	}
	c.modernMu.Lock()
	c.modernHeaders = entries
	c.modernMu.Unlock()
	return valid
}

func (c *HTTPConnection) ensureToolHeaders(ctx context.Context) error {
	c.modernListMu.Lock()
	defer c.modernListMu.Unlock()
	c.modernMu.RLock()
	listed := c.modernHeaders != nil
	c.modernMu.RUnlock()
	if listed {
		return nil
	}
	_, err := c.ListTools(ctx)
	return err
}

func (c *HTTPConnection) currentModernVersion() string {
	if v := c.modernVersion.Load(); v != nil {
		return *v
	}
	return ModernVersions[0]
}

func (c *HTTPConnection) isModern() bool {
	return c.modernVersion.Load() != nil
}

func (c *HTTPConnection) callModern(
	ctx context.Context,
	method string,
	params json.RawMessage,
) (json.RawMessage, error) {
	prepared, err := c.prepareModernParams(params)
	if err != nil {
		return nil, err
	}
	if method == "tools/call" {
		if err = c.ensureToolHeaders(ctx); err != nil {
			return nil, err
		}
	}
	result, err := c.rpc(ctx, method, prepared)
	if method != "tools/call" || !isHeaderMismatch(err) {
		return result, c.modernError(err)
	}
	if _, err = c.ListTools(ctx); err != nil {
		return nil, err
	}
	result, err = c.rpc(ctx, method, prepared)
	return result, c.modernError(err)
}

func isHeaderMismatch(err error) bool {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Code == CodeHeaderMismatch
	}
	var statusErr *HTTPStatusError
	return errors.As(err, &statusErr) && statusErr.RPC != nil && statusErr.RPC.Code == CodeHeaderMismatch
}

func (c *HTTPConnection) modernError(err error) error {
	if err == nil {
		return nil
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		if statusErr.RPC != nil {
			return c.modernRPCError(statusErr.RPC)
		}
		if statusErr.StatusCode >= 400 && statusErr.StatusCode < 500 {
			c.resetModernEra()
			return &ConnectionError{Err: err}
		}
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return c.modernRPCError(rpcErr)
	}
	return err
}

func (c *HTTPConnection) modernRPCError(err *RPCError) error {
	if err.Code == CodeUnsupportedProtocolVersion {
		c.resetModernEra()
	}
	return err
}

func (c *HTTPConnection) resetModernEra() {
	c.initMu.Lock()
	c.eraKnown = false
	c.modernVersion.Store(nil)
	c.initMu.Unlock()
}

func (c *HTTPConnection) buildModernHTTPRequest(ctx context.Context, req Request) (*http.Request, string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	sentAuth, err := c.setModernRequestHeaders(ctx, httpReq, req.Method)
	if err != nil {
		return nil, "", err
	}
	if req.Method == "tools/call" {
		c.addToolCallHeaders(httpReq, req.Params)
	}
	return httpReq, sentAuth, nil
}

func (c *HTTPConnection) setModernRequestHeaders(
	ctx context.Context,
	req *http.Request,
	method string,
) (string, error) {
	sentAuth, err := c.setRequestHeaders(ctx, req)
	if err != nil {
		return "", err
	}
	req.Header.Set("MCP-Protocol-Version", c.currentModernVersion())
	req.Header.Set("Mcp-Method", method)
	return sentAuth, nil
}

func encodeModernHeaderValue(value string) string {
	plain := !strings.HasPrefix(value, "=?base64?") || !strings.HasSuffix(value, "?=")
	plain = plain && strings.Trim(value, " \t") == value
	for i := 0; plain && i < len(value); i++ {
		plain = (value[i] >= 0x20 && value[i] <= 0x7e) || value[i] == '\t'
	}
	if plain {
		return value
	}
	return base64HeaderValue(value)
}

func base64HeaderValue(value string) string {
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}
