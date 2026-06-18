package transport

import (
	"context"
	"log/slog"
)

const maxToolsListPages = 10

func paginateToolsList(ctx context.Context, callPage func(context.Context, string) (ToolsListResult, error)) ([]ToolDefinition, error) {
	var tools []ToolDefinition
	cursor := ""
	seen := map[string]bool{}
	for range maxToolsListPages {
		r, err := callPage(ctx, cursor)
		if err != nil {
			return partialOrError(ctx, tools, err)
		}
		tools = append(tools, toToolDefs(r.Tools)...)
		next, ok := advanceCursor(seen, r.NextCursor)
		if !ok {
			return tools, nil
		}
		cursor = next
	}
	slog.Warn("tools/list: page cap reached", "pages", maxToolsListPages)
	return tools, nil
}

// Some tools beat none for mid-page server errors; context errors propagate since
// the caller explicitly aborted.
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
