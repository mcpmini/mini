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
	if _, err := s.dispatch(context.Background(), legacyDiscover, session); !errors.Is(err, errMethodNotFound) {
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
