package transport

import (
	"context"
	"encoding/json"
	"errors"
)

// Call sends an MCP request, probing the server's era and completing the initialize handshake first if needed.
func (c *HTTPConnection) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	result, _, err := c.callAndReportVersion(ctx, method, params)
	return result, err
}

func (c *HTTPConnection) callAndReportVersion(
	ctx context.Context,
	method string,
	params json.RawMessage,
) (result json.RawMessage, modernVersion string, err error) {
	modernVersion, err = c.ensureReady(ctx)
	if err != nil {
		return nil, "", err
	}
	if modernVersion == "" {
		result, err = c.callLegacy(ctx, method, params)
		return result, "", err
	}
	call := httpCall{Request: Request{Method: method, Params: params}, modernVersion: modernVersion}
	result, err = c.callModern(ctx, call)
	return result, modernVersion, err
}

func (c *HTTPConnection) callLegacy(
	ctx context.Context,
	method string,
	params json.RawMessage,
) (json.RawMessage, error) {
	if method == "tools/call" {
		params = stripModernToolCallFields(params)
	}
	return c.withSessionRecovery(ctx, func() (json.RawMessage, error) {
		return c.rpc(ctx, method, params)
	})
}

type httpCall struct {
	Request
	// Fixed for every retry so a concurrent era reset cannot send modern params with legacy headers.
	modernVersion string
}

func (c *HTTPConnection) rpc(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return c.send(ctx, httpCall{Request: Request{Method: method, Params: params}})
}

func (c *HTTPConnection) send(ctx context.Context, call httpCall) (json.RawMessage, error) {
	call.JsonRPC, call.ID = JsonRPCVersion, c.nextID.Add(1)
	return c.postWithAuthRetry(ctx, call)
}

func (c *HTTPConnection) callModern(ctx context.Context, call httpCall) (json.RawMessage, error) {
	prepared, err := prepareModernParams(call.Params, call.modernVersion)
	if err != nil {
		return nil, err
	}
	call.Params = prepared
	if call.Method == "tools/call" {
		if err = c.ensureToolHeaders(ctx); err != nil {
			return nil, err
		}
	}
	result, err := c.send(ctx, call)
	if call.Method == "tools/call" && isHeaderMismatch(err) {
		if _, err = c.ListTools(ctx); err != nil {
			return nil, err
		}
		result, err = c.send(ctx, call)
	}
	if err != nil {
		return nil, c.modernError(err)
	}
	if _, err := ResultTypeOf(result); err != nil {
		return nil, err
	}
	return result, nil
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

func (c *HTTPConnection) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	// Judged by how each page was sent, not the shared era, which a concurrent reset can change mid-listing.
	anyLegacyPage := false
	tools, err := paginateToolsList(ctx, func(ctx context.Context, cursor string) (ToolsListResult, error) {
		page, modernVersion, err := c.callToolsPage(ctx, cursor)
		anyLegacyPage = anyLegacyPage || (err == nil && modernVersion == "")
		return page, err
	})
	if err != nil {
		return nil, err
	}
	if anyLegacyPage {
		return tools, nil
	}
	return c.rebuildToolHeaders(tools), nil
}

func (c *HTTPConnection) callToolsPage(ctx context.Context, cursor string) (ToolsListResult, string, error) {
	var params json.RawMessage
	if cursor != "" {
		//nolint:errcheck // cursor map contains only strings.
		params, _ = json.Marshal(map[string]string{"cursor": cursor})
	}
	raw, modernVersion, err := c.callAndReportVersion(ctx, "tools/list", params)
	if err != nil {
		return ToolsListResult{}, "", err
	}
	r, err := decodeToolsListPage(raw)
	if err != nil {
		return ToolsListResult{}, "", err
	}
	return r, modernVersion, nil
}
