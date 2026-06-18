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
			return nil, err
		}
		tools = append(tools, toToolDefs(r.Tools)...)
		if r.NextCursor == "" {
			return tools, nil
		}
		if seen[r.NextCursor] {
			slog.Warn("tools/list: duplicate cursor, stopping pagination")
			return tools, nil
		}
		seen[r.NextCursor] = true
		cursor = r.NextCursor
	}
	slog.Warn("tools/list: page cap reached", "pages", maxToolsListPages)
	return tools, nil
}
