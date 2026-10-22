package transport

import (
	"context"
	"encoding/json"
	"errors"
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
	return c.awaitResponse(ctx, id, ch)
}

func (c *StdioConnection) awaitResponse(ctx context.Context, id int64, ch chan *Response) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		if c.pending.remove(id) && c.isModern() {
			c.cancelUpstreamRequest(context.WithoutCancel(ctx), id)
		}
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
		//nolint:errcheck // cursor map contains only strings.
		params, _ = json.Marshal(map[string]string{"cursor": cursor})
	}
	raw, err := c.Call(ctx, "tools/list", params)
	if err != nil {
		return ToolsListResult{}, err
	}
	return decodeToolsListPage(raw)
}

// A best-effort notice for a caller that has already given up, so it gets only a short bound.
const stdioCancelWriteTimeout = 250 * time.Millisecond

func (c *StdioConnection) cancelUpstreamRequest(parent context.Context, id int64) {
	ctx, cancel := clock.WithTimeout(parent, c.clock, stdioCancelWriteTimeout)
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

type deadlineWriter interface{ SetWriteDeadline(time.Time) error }

func (c *StdioConnection) writeJSONContext(ctx context.Context, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := c.lockWrite(ctx); err != nil {
		return err
	}
	defer c.unlockWrite()
	if err := ctx.Err(); err != nil {
		return err
	}
	stopInterrupt := c.interruptWriteOnCancel(ctx)
	_, writeErr := fmt.Fprintf(c.stdin, "%s\n", encoded)
	if err := stopInterrupt(); err != nil {
		return err
	}
	if writeErr != nil {
		return fmt.Errorf("write to upstream: %w", writeErr)
	}
	return nil
}

func (c *StdioConnection) interruptWriteOnCancel(ctx context.Context) (stop func() error) {
	pipe, ok := c.stdin.(deadlineWriter)
	if !ok {
		return func() error { return nil }
	}
	interrupted := make(chan error, 1)
	stopAfter := context.AfterFunc(ctx, func() {
		// A child that stops reading stdin blocks the write once the pipe fills; a past deadline
		// unblocks it without killing the child.
		err := pipe.SetWriteDeadline(time.Unix(1, 0))
		interrupted <- err
		if err != nil {
			c.abandon() // ending the connection closes stdin, which fails the blocked write
		}
	})
	return func() error {
		if stopAfter() {
			return nil
		}
		if err := <-interrupted; err != nil {
			return fmt.Errorf("interrupt upstream write: %w", err)
		}
		if err := pipe.SetWriteDeadline(time.Time{}); err != nil {
			return fmt.Errorf("reset upstream write deadline: %w", err)
		}
		return nil
	}
}

func (c *StdioConnection) lockWrite(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.writeGate:
		return nil
	}
}

func (c *StdioConnection) unlockWrite() { c.writeGate <- struct{}{} }

func (c *StdioConnection) abandonAfterFailedWrite(ctx context.Context, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return // the write never started
	}
	c.abandon() // a partial line would break the framing of every later message
}
