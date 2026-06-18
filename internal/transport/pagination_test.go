package transport

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func makeTool(name string) MCPTool {
	return MCPTool{Name: name}
}

func toolNames(defs []ToolDefinition) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

func TestPaginateToolsList_singlePage(t *testing.T) {
	calls := 0
	callPage := func(_ context.Context, cursor string) (ToolsListResult, error) {
		calls++
		return ToolsListResult{Tools: []MCPTool{makeTool("a"), makeTool("b")}}, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if want := []string{"a", "b"}; !slices.Equal(toolNames(got), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got), want)
	}
}

func TestPaginateToolsList_twoPages(t *testing.T) {
	var receivedCursors []string
	callPage := func(_ context.Context, cursor string) (ToolsListResult, error) {
		receivedCursors = append(receivedCursors, cursor)
		if cursor == "" {
			return ToolsListResult{Tools: []MCPTool{makeTool("a")}, NextCursor: "page2"}, nil
		}
		return ToolsListResult{Tools: []MCPTool{makeTool("b")}}, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"", "page2"}; !slices.Equal(receivedCursors, want) {
		t.Errorf("cursors: got %v, want %v", receivedCursors, want)
	}
	if want := []string{"a", "b"}; !slices.Equal(toolNames(got), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got), want)
	}
}

func TestPaginateToolsList_threePages(t *testing.T) {
	cursors := map[string]string{"": "page2", "page2": "page3"}
	tools := map[string]string{"": "a", "page2": "b", "page3": "c"}
	callPage := func(_ context.Context, cursor string) (ToolsListResult, error) {
		r := ToolsListResult{Tools: []MCPTool{makeTool(tools[cursor])}}
		if next, ok := cursors[cursor]; ok {
			r.NextCursor = next
		}
		return r, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(toolNames(got), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got), want)
	}
}

func TestPaginateToolsList_maxPagesReached(t *testing.T) {
	calls := 0
	callPage := func(_ context.Context, cursor string) (ToolsListResult, error) {
		calls++
		nextCursor := "page" + string(rune('0'+calls))
		return ToolsListResult{
			Tools:      []MCPTool{makeTool("t")},
			NextCursor: nextCursor,
		}, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if calls != maxToolsListPages {
		t.Errorf("expected %d calls, got %d", maxToolsListPages, calls)
	}
	if len(got) != maxToolsListPages {
		t.Errorf("expected %d tools, got %d", maxToolsListPages, len(got))
	}
}

func TestPaginateToolsList_duplicateCursor(t *testing.T) {
	calls := 0
	callPage := func(_ context.Context, cursor string) (ToolsListResult, error) {
		calls++
		return ToolsListResult{
			Tools:      []MCPTool{makeTool("t")},
			NextCursor: "stuck",
		}, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 tools, got %d", len(got))
	}
}

func TestPaginateToolsList_emptyPageWithCursor(t *testing.T) {
	callPage := func(_ context.Context, cursor string) (ToolsListResult, error) {
		if cursor == "" {
			return ToolsListResult{NextCursor: "page2"}, nil
		}
		return ToolsListResult{Tools: []MCPTool{makeTool("a")}}, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a"}; !slices.Equal(toolNames(got), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got), want)
	}
}

func TestPaginateToolsList_contextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	callPage := func(ctx context.Context, cursor string) (ToolsListResult, error) {
		calls++
		if calls == 1 {
			cancel()
			return ToolsListResult{Tools: []MCPTool{makeTool("a")}, NextCursor: "page2"}, nil
		}
		return ToolsListResult{}, ctx.Err()
	}
	_, err := paginateToolsList(ctx, callPage)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
