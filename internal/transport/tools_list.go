package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

const maxToolsListPages = 10

type ToolList struct {
	Tools []ToolDefinition
}

func decodeToolsListPage(raw json.RawMessage) (ToolsListResult, error) {
	if err := requireCompleteResult(raw, "tools/list"); err != nil {
		return ToolsListResult{}, err
	}
	var result ToolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ToolsListResult{}, fmt.Errorf("parse tools/list: %w", err)
	}
	return result, nil
}

func paginateToolsList(
	ctx context.Context,
	callPage func(context.Context, string) (ToolsListResult, error),
) ([]ToolDefinition, error) {
	var tools []ToolDefinition
	cursor := ""
	seen := map[string]bool{}
	for range maxToolsListPages {
		r, err := callPage(ctx, cursor)
		if err != nil {
			return partialOrError(ctx, tools, err)
		}
		tools = append(tools, r.Tools...)
		next, ok := advanceCursor(seen, r.NextCursor)
		if !ok {
			return tools, nil
		}
		cursor = next
	}
	slog.Warn("tools/list: page cap reached", "pages", maxToolsListPages)
	return tools, nil
}

// Partial results are more useful than total failure — an agent missing a few tools can still work.
func partialOrError(ctx context.Context, tools []ToolDefinition, err error) ([]ToolDefinition, error) {
	if len(tools) > 0 && ctx.Err() == nil {
		slog.Warn("tools/list: error mid-pagination, returning partial results", "err", err)
		return tools, nil
	}
	return nil, err
}

func advanceCursor(seen map[string]bool, next string) (string, bool) {
	if next == "" {
		return "", false
	}
	if seen[next] {
		slog.Warn("tools/list: duplicate cursor, stopping pagination")
		return "", false
	}
	seen[next] = true
	return next, true
}
