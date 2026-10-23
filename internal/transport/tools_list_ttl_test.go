//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestToolsListTTLDecodesOnBothTransports(t *testing.T) {
	for _, ttl := range []string{"1000", "0", "-1", "null", `"bad"`, "1.5", "9223372036854775808", "9223372036854775807", ""} {
		t.Run(ttl, func(t *testing.T) {
			raw := `{"tools":[{"name":"kept","inputSchema":{}}]`
			if ttl != "" {
				raw += `,"ttlMs":` + ttl
			}
			raw += `}`
			fc := clock.NewFake()
			want := Freshness{managed: true, until: fc.Now()}
			if ttl == "1000" {
				want.until = want.until.Add(time.Second)
			}
			conn, w, r := makePipeConn(t)
			conn.clock = fc
			conn.setModern(ModernVersions[0])
			scriptStdioUpstream(w, r, replyResult(w, json.RawMessage(raw)))
			toolList, err := conn.ListTools(t.Context())
			assertKeptToolList(t, keptToolList{ToolList: toolList, Err: err, Freshness: want})

			srv := newModernServer(t, modernUpstream{Handle: func(w http.ResponseWriter, r *http.Request, req Request) {
				writeRPCResult(t, rpcResultParams{Writer: w, ID: req.ID, Result: json.RawMessage(raw)})
			}})
			toolList, err = mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: fc}).ListTools(t.Context())
			assertKeptToolList(t, keptToolList{ToolList: toolList, Err: err, Freshness: want})
		})
	}
}

func TestToolsListLegacyPagesStayUnmanaged(t *testing.T) {
	const raw = `{"tools":[{"name":"kept","inputSchema":{}}],"ttlMs":1000}`
	conn, w, r := makePipeConn(t)
	scriptStdioUpstream(w, r, replyResult(w, json.RawMessage(raw)))
	toolList, err := conn.ListTools(t.Context())
	assertKeptToolList(t, keptToolList{ToolList: toolList, Err: err})

	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
			t.Errorf("decode request: %v", decodeErr)
			return
		}
		switch req.Method {
		case "initialize":
			result := json.RawMessage(`{"protocolVersion":"` + LegacyVersions[0] + `"}`)
			writeRPCResult(t, rpcResultParams{Writer: w, ID: req.ID, Result: result})
		case "tools/list":
			writeRPCResult(t, rpcResultParams{Writer: w, ID: req.ID, Result: json.RawMessage(raw)})
		}
	})
	toolList, err = mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: srv.URL}).ListTools(t.Context())
	assertKeptToolList(t, keptToolList{ToolList: toolList, Err: err})
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
			list, err := paginateToolsList(context.Background(), func(context.Context, string) (toolsListPage, error) {
				calls++
				switch {
				case tc.after != nil:
					return tc.after(calls), nil
				case calls > len(tc.pages):
					return toolsListPage{}, errors.New("page failed")
				default:
					return tc.pages[calls-1], nil
				}
			})
			if err != nil || list.Freshness != tc.want {
				t.Fatalf("freshness = %+v, %v; want %+v", list.Freshness, err, tc.want)
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
	done := make(chan keptToolList, 1)
	go func() {
		toolList, err := conn.ListTools(t.Context())
		done <- keptToolList{ToolList: toolList, Err: err}
	}()
	<-entered
	conn.resetModernEra()
	close(release)
	got := <-done
	got.Freshness = Freshness{managed: true, until: fc.Now().Add(time.Second)}
	assertKeptToolList(t, got)
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
			assertKeptToolList(t, keptToolList{ToolList: toolList, Err: err, Freshness: want})
		})
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

type keptToolList struct {
	ToolList  ToolList
	Err       error
	Freshness Freshness
}

func assertKeptToolList(t *testing.T, got keptToolList) {
	t.Helper()
	list := got.ToolList
	if got.Err != nil || len(list.Tools) != 1 || list.Tools[0].Name != "kept" || list.Freshness != got.Freshness {
		t.Fatalf("tool list = %+v, %v; want the kept tool with freshness %+v", list, got.Err, got.Freshness)
	}
}
