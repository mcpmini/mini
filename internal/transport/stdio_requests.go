package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func (c *StdioConnection) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	modernVersion := c.negotiatedModernVersion()
	if modernVersion != "" {
		prepared, err := prepareModernParams(params, modernVersion)
		if err != nil {
			return nil, err
		}
		params = prepared
	} else if method == "tools/call" {
		params = stripModernToolCallFields(params)
	}
	result, err := c.rpc(ctx, method, params)
	if err != nil || modernVersion == "" {
		return result, err
	}
	if _, err := ResultTypeOf(result); err != nil {
		return nil, err
	}
	return result, nil
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
	return c.awaitResponse(ctx, awaitResponseParams{ID: id, Method: method, Response: ch})
}

type awaitResponseParams struct {
	ID       int64
	Method   string
	Response chan *Response
}

func (c *StdioConnection) awaitResponse(ctx context.Context, p awaitResponseParams) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		if c.pending.remove(p.ID) && p.Method != "initialize" {
			c.cancelUpstreamRequest(context.WithoutCancel(ctx), p.ID)
		}
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	case resp := <-p.Response:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *StdioConnection) ListTools(ctx context.Context) (ToolList, error) {
	return paginateToolsList(ctx, c.callToolsPage)
}

func (c *StdioConnection) callToolsPage(ctx context.Context, cursor string) (toolsListPage, error) {
	var params json.RawMessage
	if cursor != "" {
		//nolint:errcheck // cursor map contains only strings.
		params, _ = json.Marshal(map[string]string{"cursor": cursor})
	}
	raw, err := c.Call(ctx, "tools/list", params)
	if err != nil {
		return toolsListPage{}, err
	}
	return decodeToolsListPage(raw, c.clock.Now(), c.negotiatedModernVersion())
}

// A best-effort notice for a caller that has already given up, so it gets only a short bound.
const upstreamCancelTimeout = 250 * time.Millisecond

func (c *StdioConnection) cancelUpstreamRequest(parent context.Context, id int64) {
	ctx, cancel := clock.WithTimeout(parent, c.clock, upstreamCancelTimeout)
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
