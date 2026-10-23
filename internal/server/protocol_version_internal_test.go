//go:build test

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/version"
)

func TestAddModernResultFieldsAddsAndPreservesServerIdentity(t *testing.T) {
	result, err := addModernResultFields("tools/list", map[string]any{"tools": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	fields := result.(map[string]json.RawMessage)
	var meta map[string]json.RawMessage
	if err = json.Unmarshal(fields["_meta"], &meta); err != nil {
		t.Fatal(err)
	}
	var identity transport.ServerInfo
	if err = json.Unmarshal(meta["io.modelcontextprotocol/serverInfo"], &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Name != "mini" || identity.Version != version.Version {
		t.Fatalf("generated server identity = %+v", identity)
	}

	const upstreamIdentity = `{"name":"upstream","version":"9"}`
	upstreamResult := json.RawMessage(
		`{"_meta":{"trace":"keep","io.modelcontextprotocol/serverInfo":` + upstreamIdentity + `}}`,
	)
	result, err = addModernResultFields("tools/list", upstreamResult)
	if err != nil {
		t.Fatal(err)
	}
	fields = result.(map[string]json.RawMessage)
	if err := json.Unmarshal(fields["_meta"], &meta); err != nil {
		t.Fatal(err)
	}
	if string(meta["trace"]) != `"keep"` || string(meta["io.modelcontextprotocol/serverInfo"]) != upstreamIdentity {
		t.Fatalf("upstream metadata was not preserved: %s", fields["_meta"])
	}
}

func TestAddModernResultFieldsPreservesTypedUpstreamResultsByteForByte(t *testing.T) {
	for _, raw := range []string{
		`{"resultType":"complete", "opaque": [ 1, 2 ]}`,
		`{"resultType":"input_required","inputRequests":{"r":{}},"requestState":{"token":"keep"}}`,
	} {
		got, err := addModernResultFields("tools/call", json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.(json.RawMessage), []byte(raw)) {
			t.Fatalf("typed upstream result changed: got %s, want %s", got, raw)
		}
	}
}

func TestToolCallRequestMRTRUsesItsDeclaredVersion(t *testing.T) {
	modern := toolCallRequest{
		Meta: transport.RequestMeta{
			ProtocolVersion: transport.ModernVersions[0],
			ClientCapabilities: &transport.ClientCapabilities{
				Elicitation: json.RawMessage(`{"url":"/elicitation"}`),
			},
		},
		InputResponses: json.RawMessage(`{"answer":"yes"}`),
		RequestState:   json.RawMessage(`{"cursor":"next"}`),
	}
	if mrtr := modern.mrtr(); mrtr == nil || string(mrtr.InputResponses) != `{"answer":"yes"}` ||
		string(mrtr.RequestState) != `{"cursor":"next"}` {
		t.Fatalf("modern MRTR = %+v", mrtr)
	}
	legacy := modern
	legacy.Meta.ProtocolVersion = transport.LatestLegacyVersion
	if mrtr := legacy.mrtr(); mrtr != nil {
		t.Fatalf("legacy request gained MRTR: %+v", mrtr)
	}
}

func TestRequestVersionControlsDispatchAndResultDecoration(t *testing.T) {
	s := &Server{
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		toolMode:    transport.ToolModeCompact,
		toolSchemas: []map[string]any{},
	}
	session := newSession(clock.NewFake())
	modern := transport.Request{
		JsonRPC: transport.JsonRPCVersion,
		ID:      1,
		Method:  "tools/list",
		Params: json.RawMessage(
			`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":{}}}`,
		),
	}
	modernResponse, send := s.handleRequest(context.Background(), modern, session)
	if !send || modernResponse.Error != nil {
		t.Fatalf("modern tools/list response = %+v", modernResponse)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(modernResponse.Result, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["resultType"]) != `"complete"` {
		t.Fatalf("modern tools/list result = %s", modernResponse.Result)
	}

	legacyDiscover := transport.Request{
		JsonRPC: transport.JsonRPCVersion,
		ID:      2,
		Method:  "server/discover",
		Params:  json.RawMessage(`{}`),
	}
	if _, err := s.routeRequest(context.Background(), legacyDiscover, session); !errors.Is(err, errMethodNotFound) {
		t.Fatalf("legacy discover after modern request error = %v, want method not found", err)
	}
	legacyList := transport.Request{
		JsonRPC: transport.JsonRPCVersion,
		ID:      3,
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	}
	legacyResponse, send := s.handleRequest(context.Background(), legacyList, session)
	if !send || legacyResponse.Error != nil {
		t.Fatalf("legacy tools/list response = %+v", legacyResponse)
	}
	fields = nil
	if err := json.Unmarshal(legacyResponse.Result, &fields); err != nil {
		t.Fatal(err)
	}
	if _, found := fields["resultType"]; found {
		t.Fatalf("legacy tools/list inherited modern result fields: %s", legacyResponse.Result)
	}
}

func TestAddModernResultFieldsRejectsNonObjectResults(t *testing.T) {
	for _, raw := range []string{`[1]`, `null`, `"text"`, `{"_meta":[]}`, `{"_meta":"x"}`} {
		t.Run(raw, func(t *testing.T) {
			if got, err := addModernResultFields("tools/call", json.RawMessage(raw)); err == nil {
				t.Fatalf("decorated non-object result %s as %v, want an error", raw, got)
			}
		})
	}
}

func TestAddModernResultFieldsDecoratesPerMethod(t *testing.T) {
	decorate := func(method, raw string) map[string]json.RawMessage {
		t.Helper()
		result, err := addModernResultFields(method, json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		return result.(map[string]json.RawMessage)
	}
	t.Run("only tools/list is cacheable", func(t *testing.T) {
		fields := decorate("ping", `{}`)
		if string(fields["resultType"]) != `"complete"` || fields["ttlMs"] != nil || fields["cacheScope"] != nil {
			t.Fatalf("ping result = %v, want resultType only", fields)
		}
	})
	t.Run("tools/list keeps its own fields and gains the missing ones", func(t *testing.T) {
		fields := decorate("tools/list", `{"resultType":"complete","ttlMs":5000}`)
		if string(fields["ttlMs"]) != `5000` || string(fields["cacheScope"]) != `"private"` {
			t.Fatalf("tools/list result = %v, want ttlMs 5000 kept and cacheScope added", fields)
		}
	})
	t.Run("null _meta gains the server identity", func(t *testing.T) {
		var meta map[string]json.RawMessage
		if err := json.Unmarshal(decorate("ping", `{"_meta":null}`)["_meta"], &meta); err != nil {
			t.Fatal(err)
		}
		if meta["io.modelcontextprotocol/serverInfo"] == nil {
			t.Fatalf("_meta = %v, want mini's server identity", meta)
		}
	})
}

func TestActivateModern(t *testing.T) {
	s := newRouteTestServer(transport.ToolModeCompact)
	t.Run("stdio session needs no initialize", func(t *testing.T) {
		session := newSession(clock.NewFake())
		s.activateModern(session)
		select {
		case <-session.initDone:
		default:
			t.Fatal("modern request left a stdio session waiting for initialize")
		}
		if !session.clientReady.Load() || session.toolMode() != transport.ToolModeCompact || !session.modern.Load() {
			t.Fatalf("activated session: ready=%v mode=%v modern=%v, want ready, the server's mode, modern",
				session.clientReady.Load(), session.toolMode(), session.modern.Load())
		}
	})
	t.Run("HTTP session keeps its own lifecycle", func(t *testing.T) {
		session := newSession(clock.NewFake())
		session.httpSession.Store(true)
		s.activateModern(session)
		select {
		case <-session.initDone:
			t.Fatal("modern request initialized an HTTP session")
		default:
		}
		if session.clientReady.Load() {
			t.Fatal("modern request marked an HTTP session ready for notifications")
		}
	})
}

func TestModernVersionError(t *testing.T) {
	request := func(params string) transport.Request {
		return transport.Request{JsonRPC: transport.JsonRPCVersion, ID: 4, Params: json.RawMessage(params)}
	}
	for _, params := range []string{`{}`, `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`} {
		if resp := modernVersionError(request(params)); resp != nil {
			t.Errorf("params %s rejected: %+v", params, resp.Error)
		}
	}
	legacy := request(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + transport.LatestLegacyVersion + `"}}`)
	if resp := modernVersionError(legacy); resp == nil || resp.ID != 4 ||
		resp.Error.Code != transport.CodeUnsupportedProtocolVersion {
		t.Fatalf("legacy version in request metadata = %+v, want unsupported protocol version for id 4", resp)
	}
}

func TestHandleDiscoverFollowsSessionToolMode(t *testing.T) {
	for _, tc := range []struct {
		session, server transport.ToolMode
		want            string
	}{
		{session: transport.ToolModeProxy, server: transport.ToolModeCompact, want: proxyInitInstructions},
		{session: transport.ToolModeCompact, server: transport.ToolModeProxy, want: compactInitInstructions},
	} {
		session := newSession(clock.NewFake())
		session.setToolModeOnce(tc.session)
		if got := newRouteTestServer(tc.server).handleDiscover(session).Instructions; got != tc.want {
			t.Errorf("%v session on a %v server: discover instructions = %q, want %q",
				tc.session, tc.server, got, tc.want)
		}
	}
}
