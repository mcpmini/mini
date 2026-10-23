package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"
)

const maxToolsListPages = 10

type ToolList struct {
	Tools     []ToolDefinition
	Freshness Freshness
}

// Freshness is how long an upstream says its tool list stays current (MCP 2026-07-28 ttlMs). The
// zero value is unmanaged: the upstream gives no lifetime, so the list stays current until a
// change notification.
type Freshness struct {
	managed bool
	until   time.Time
}

func (f Freshness) Stale(now time.Time) bool {
	return f.managed && !now.Before(f.until)
}

// Expire makes a managed list stale; an unmanaged list stays unmanaged.
func (f Freshness) Expire() Freshness {
	if !f.managed {
		return f
	}
	return Freshness{managed: true}
}

func (f Freshness) combine(page Freshness) Freshness {
	switch {
	case !f.managed || !page.managed:
		return Freshness{}
	case page.until.Before(f.until):
		return page
	default:
		return f
	}
}

type toolsListPage struct {
	Tools      []ToolDefinition
	NextCursor string
	freshness  Freshness
}

func decodeToolsListPage(raw json.RawMessage, received time.Time, modernVersion string) (toolsListPage, error) {
	if err := requireCompleteResult(raw, "tools/list"); err != nil {
		return toolsListPage{}, err
	}
	var result ToolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return toolsListPage{}, fmt.Errorf("parse tools/list: %w", err)
	}
	page := toolsListPage{Tools: result.Tools, NextCursor: result.NextCursor}
	if modernVersion != "" {
		page.freshness = Freshness{managed: true, until: received.Add(ttlDuration(result.TTLMs))}
	}
	return page, nil
}

// A missing, negative or malformed ttlMs counts as 0 (immediately stale) rather than failing the
// page, so a bad hint never costs the agent its tools.
func ttlDuration(raw json.RawMessage) time.Duration {
	var millis int64
	err := json.Unmarshal(raw, &millis)
	if err != nil || millis < 0 || millis > math.MaxInt64/int64(time.Millisecond) {
		return 0
	}
	return time.Duration(millis) * time.Millisecond
}

func paginateToolsList(
	ctx context.Context,
	callPage func(context.Context, string) (toolsListPage, error),
) (ToolList, error) {
	var toolList ToolList
	cursor := ""
	seen := map[string]bool{}
	for i := range maxToolsListPages {
		page, err := callPage(ctx, cursor)
		if err != nil {
			return partialOrError(ctx, toolList, err)
		}
		toolList.addPage(i, page)
		if page.NextCursor == "" {
			return toolList, nil
		}
		next, ok := advanceCursor(seen, page.NextCursor)
		if !ok {
			return expireIncomplete(toolList), nil
		}
		cursor = next
	}
	slog.Warn("tools/list: page cap reached", "pages", maxToolsListPages)
	return expireIncomplete(toolList), nil
}

func (l *ToolList) addPage(index int, page toolsListPage) {
	l.Tools = append(l.Tools, page.Tools...)
	if index == 0 {
		l.Freshness = page.freshness
		return
	}
	l.Freshness = l.Freshness.combine(page.freshness)
}

func expireIncomplete(toolList ToolList) ToolList {
	toolList.Freshness = toolList.Freshness.Expire()
	return toolList
}

// Partial results are more useful than total failure — an agent missing a few tools can still work.
func partialOrError(ctx context.Context, toolList ToolList, err error) (ToolList, error) {
	if len(toolList.Tools) > 0 && ctx.Err() == nil {
		slog.Warn("tools/list: error mid-pagination, returning partial results", "err", err)
		return expireIncomplete(toolList), nil
	}
	return ToolList{}, err
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
