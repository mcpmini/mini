//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func makeTool(name string) ToolDefinition {
	return ToolDefinition{Name: name}
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
	callPage := func(_ context.Context, cursor string) (toolsListPage, error) {
		calls++
		return toolsListPage{Tools: []ToolDefinition{makeTool("a"), makeTool("b")}}, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if want := []string{"a", "b"}; !slices.Equal(toolNames(got.Tools), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got.Tools), want)
	}
}

func TestPaginateToolsList_twoPages(t *testing.T) {
	var receivedCursors []string
	callPage := func(_ context.Context, cursor string) (toolsListPage, error) {
		receivedCursors = append(receivedCursors, cursor)
		if cursor == "" {
			return toolsListPage{Tools: []ToolDefinition{makeTool("a")}, NextCursor: "page2"}, nil
		}
		return toolsListPage{Tools: []ToolDefinition{makeTool("b")}}, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"", "page2"}; !slices.Equal(receivedCursors, want) {
		t.Errorf("cursors: got %v, want %v", receivedCursors, want)
	}
	if want := []string{"a", "b"}; !slices.Equal(toolNames(got.Tools), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got.Tools), want)
	}
}

func TestPaginateToolsList_threePages(t *testing.T) {
	cursors := map[string]string{"": "page2", "page2": "page3"}
	tools := map[string]string{"": "a", "page2": "b", "page3": "c"}
	callPage := func(_ context.Context, cursor string) (toolsListPage, error) {
		r := toolsListPage{Tools: []ToolDefinition{makeTool(tools[cursor])}}
		if next, ok := cursors[cursor]; ok {
			r.NextCursor = next
		}
		return r, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(toolNames(got.Tools), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got.Tools), want)
	}
}

func TestPaginateToolsList_maxPagesReached(t *testing.T) {
	calls := 0
	callPage := func(_ context.Context, cursor string) (toolsListPage, error) {
		calls++
		nextCursor := "page" + string(rune('0'+calls))
		return toolsListPage{
			Tools:      []ToolDefinition{makeTool("t")},
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
	if len(got.Tools) != maxToolsListPages {
		t.Errorf("expected %d tools, got %d", maxToolsListPages, len(got.Tools))
	}
}

func TestPaginateToolsList_duplicateCursor(t *testing.T) {
	calls := 0
	callPage := func(_ context.Context, cursor string) (toolsListPage, error) {
		calls++
		return toolsListPage{
			Tools:      []ToolDefinition{makeTool("t")},
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
	if len(got.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(got.Tools))
	}
}

func TestPaginateToolsList_emptyPageWithCursor(t *testing.T) {
	callPage := func(_ context.Context, cursor string) (toolsListPage, error) {
		if cursor == "" {
			return toolsListPage{NextCursor: "page2"}, nil
		}
		return toolsListPage{Tools: []ToolDefinition{makeTool("a")}}, nil
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a"}; !slices.Equal(toolNames(got.Tools), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got.Tools), want)
	}
}

func TestPaginateToolsList_errorOnFirstPage(t *testing.T) {
	sentinel := errors.New("upstream unavailable")
	callPage := func(_ context.Context, _ string) (toolsListPage, error) {
		return toolsListPage{}, sentinel
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
	if got.Tools != nil {
		t.Errorf("expected nil tools, got %v", got.Tools)
	}
}

func TestPaginateToolsList_errorOnSubsequentPage(t *testing.T) {
	sentinel := errors.New("page 2 failed")
	calls := 0
	callPage := func(_ context.Context, _ string) (toolsListPage, error) {
		calls++
		if calls == 1 {
			return toolsListPage{Tools: []ToolDefinition{makeTool("a")}, NextCursor: "page2"}, nil
		}
		return toolsListPage{}, sentinel
	}
	got, err := paginateToolsList(context.Background(), callPage)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if want := []string{"a"}; !slices.Equal(toolNames(got.Tools), want) {
		t.Errorf("tools: got %v, want %v", toolNames(got.Tools), want)
	}
}

func TestPaginateToolsList_contextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	callPage := func(ctx context.Context, cursor string) (toolsListPage, error) {
		calls++
		if calls == 1 {
			cancel()
			return toolsListPage{Tools: []ToolDefinition{makeTool("a")}, NextCursor: "page2"}, nil
		}
		return toolsListPage{}, ctx.Err()
	}
	_, err := paginateToolsList(ctx, callPage)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestPaginationReturnsOnlyVerifiedEarlierPages(t *testing.T) {
	page := 0
	first := json.RawMessage(`{"tools":[{"name":"verified","inputSchema":{}}],"nextCursor":"next"}`)
	second := json.RawMessage(`{"resultType":"future","tools":[{"name":"invalid","inputSchema":{}}]}`)
	got, err := paginateToolsList(context.Background(), func(context.Context, string) (toolsListPage, error) {
		page++
		if page == 1 {
			return decodeToolsListPage(first, time.Time{}, "")
		}
		return decodeToolsListPage(second, time.Time{}, "")
	})
	if err != nil || !slices.Equal(toolNames(got.Tools), []string{"verified"}) {
		t.Fatalf("pagination = %v, %v; want only verified first page", toolNames(got.Tools), err)
	}
}

func TestDecodeToolsListPageFreshness(t *testing.T) {
	received := time.Unix(100, 0)
	expired := Freshness{managed: true, until: received}
	for _, tc := range []struct {
		ttl           string
		modernVersion string
		want          Freshness
	}{
		{ttl: "1000", modernVersion: ModernVersions[0], want: Freshness{managed: true, until: received.Add(time.Second)}},
		{ttl: "1000", want: Freshness{}},
		{ttl: "", modernVersion: ModernVersions[0], want: expired},
		{ttl: "0", modernVersion: ModernVersions[0], want: expired},
		{ttl: "-1", modernVersion: ModernVersions[0], want: expired},
		{ttl: "null", modernVersion: ModernVersions[0], want: expired},
		{ttl: `"bad"`, modernVersion: ModernVersions[0], want: expired},
		{ttl: "1.5", modernVersion: ModernVersions[0], want: expired},
		{ttl: "9223372036854775807", modernVersion: ModernVersions[0], want: expired},
		{ttl: "9223372036854775808", modernVersion: ModernVersions[0], want: expired},
	} {
		t.Run(tc.ttl+" "+tc.modernVersion, func(t *testing.T) {
			raw := `{"tools":[{"name":"kept","inputSchema":{}}]`
			if tc.ttl != "" {
				raw += `,"ttlMs":` + tc.ttl
			}
			page, err := decodeToolsListPage(json.RawMessage(raw+`}`), received, tc.modernVersion)
			if err != nil || len(page.Tools) != 1 || page.freshness != tc.want {
				t.Fatalf("page = %+v, %v; want the kept tool with freshness %+v", page, err, tc.want)
			}
		})
	}
}

func TestToolsListFreshnessAcrossPages(t *testing.T) {
	base := time.Unix(100, 0)
	freshFor := func(seconds int) Freshness {
		return Freshness{managed: true, until: base.Add(time.Duration(seconds) * time.Second)}
	}
	expired := Freshness{managed: true}
	page := func(next string, freshness Freshness) toolsListPage {
		return toolsListPage{Tools: []ToolDefinition{{Name: "kept"}}, NextCursor: next, freshness: freshness}
	}
	everyPageContinues := func(call int) toolsListPage { return page(string(rune('a'+call)), freshFor(1)) }
	for _, tc := range []struct {
		name  string
		pages []toolsListPage
		after func(call int) toolsListPage
		want  Freshness
	}{
		{name: "earliest page wins", pages: []toolsListPage{page("b", freshFor(2)), page("", freshFor(1))}, want: freshFor(1)},
		{name: "empty list", pages: []toolsListPage{{freshness: freshFor(1)}}, want: freshFor(1)},
		{name: "later page fails", pages: []toolsListPage{page("b", freshFor(1))}, want: expired},
		{name: "duplicate cursor", pages: []toolsListPage{page("b", freshFor(1)), page("b", freshFor(1))}, want: expired},
		{name: "page cap", after: everyPageContinues, want: expired},
		{name: "legacy page", pages: []toolsListPage{page("b", freshFor(1)), page("", Freshness{})}},
		{name: "legacy then failure", pages: []toolsListPage{page("b", Freshness{})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			toolList, err := paginateToolsList(
				context.Background(),
				func(context.Context, string) (toolsListPage, error) {
					calls++
					switch {
					case tc.after != nil:
						return tc.after(calls), nil
					case calls > len(tc.pages):
						return toolsListPage{}, errors.New("page failed")
					default:
						return tc.pages[calls-1], nil
					}
				},
			)
			if err != nil || toolList.Freshness != tc.want {
				t.Fatalf("freshness = %+v, %v; want %+v", toolList.Freshness, err, tc.want)
			}
		})
	}
}

func TestToolsListPagesUseTheirOwnReceiptTimes(t *testing.T) {
	for _, kind := range []string{"stdio", "http"} {
		t.Run(kind, func(t *testing.T) {
			fc := clock.NewFake()
			start := fc.Now()
			calls := 0
			page := func() json.RawMessage {
				calls++
				if calls == 1 {
					return json.RawMessage(`{"tools":[],"ttlMs":10000,"nextCursor":"next"}`)
				}
				fc.Advance(2 * time.Second)
				return json.RawMessage(`{"tools":[{"name":"kept","inputSchema":{}}],"ttlMs":1000}`)
			}
			var toolList ToolList
			var err error
			if kind == "stdio" {
				conn, w, r := makePipeConn(t)
				conn.clock = fc
				conn.setModern(ModernVersions[0])
				reply := func(req Request) { replyResult(w, page())(req) }
				scriptStdioUpstream(w, r, reply, reply)
				toolList, err = conn.ListTools(t.Context())
			} else {
				reply := func(w http.ResponseWriter, r *http.Request, req Request) {
					writeRPCResult(t, rpcResultParams{Writer: w, ID: req.ID, Result: page()})
				}
				srv := newModernServer(t, modernUpstream{Handle: reply})
				toolList, err = mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: fc}).ListTools(t.Context())
			}
			want := Freshness{managed: true, until: start.Add(3 * time.Second)}
			if err != nil || len(toolList.Tools) != 1 || toolList.Freshness != want {
				t.Fatalf("tool list = %+v, %v; want the kept tool with freshness %+v", toolList, err, want)
			}
		})
	}
}

func TestToolsListHTTPKeepsRequestEraAcrossReset(t *testing.T) {
	fc := clock.NewFake()
	entered, release := make(chan struct{}), make(chan struct{})
	srv := newModernServer(t, modernUpstream{Handle: func(w http.ResponseWriter, r *http.Request, req Request) {
		close(entered)
		<-release
		result := json.RawMessage(`{"tools":[{"name":"kept","inputSchema":{}}],"ttlMs":1000}`)
		writeRPCResult(t, rpcResultParams{Writer: w, ID: req.ID, Result: result})
	}})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: fc})
	done := make(chan ToolList, 1)
	go func() {
		toolList, _ := conn.ListTools(t.Context())
		done <- toolList
	}()
	<-entered
	conn.resetModernEra()
	close(release)
	if got, want := (<-done).Freshness, (Freshness{managed: true, until: fc.Now().Add(time.Second)}); got != want {
		t.Fatalf("freshness after an era reset mid-request = %+v, want %+v", got, want)
	}
}

func TestFreshnessStaleAndExpire(t *testing.T) {
	now := time.Unix(100, 0)
	managed := Freshness{managed: true, until: now.Add(time.Second)}
	if (Freshness{}).Stale(now.Add(time.Hour)) || (Freshness{}).Expire() != (Freshness{}) {
		t.Fatal("an unmanaged list became stale")
	}
	if managed.Stale(now) || !managed.Stale(now.Add(time.Second)) {
		t.Fatal("a managed list was stale before its deadline or fresh at it")
	}
	if !managed.Expire().Stale(now) {
		t.Fatal("an expired list was still fresh")
	}
}

func TestDecodeToolsListPageRejectsAMalformedPage(t *testing.T) {
	if page, err := decodeToolsListPage(json.RawMessage(`{"tools":"none"}`), time.Time{}, ""); err == nil {
		t.Fatalf("malformed page decoded as %+v, want an error", page)
	}
}
