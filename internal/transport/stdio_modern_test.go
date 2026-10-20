//go:build test

package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestStdioModernDiscoverySendsModernMetadata(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	seen := scriptStdioUpstream(
		serverW,
		serverR,
		replyResult(serverW, DiscoverResult{SupportedVersions: ModernVersions}),
	)
	if _, err := conn.negotiate(context.Background()); err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	discover := <-seen
	if discover.Method != "server/discover" {
		t.Fatalf("first request = %q, want server/discover", discover.Method)
	}
	assertRequestMeta(t, discover.Params, ModernVersions[0], false)
}

const modernContinuationCall = `{"name":"confirm","arguments":{},"inputResponses":{"r1":{"accepted":true}},` +
	`"requestState":9007199254740993,"custom":1,` +
	`"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},"unknown":true}}`

func TestStdioModernCallKeepsContinuationFieldsAndMetadata(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	conn.setModern(ModernVersions[0])
	seen := scriptStdioUpstream(serverW, serverR, replyResult(serverW, json.RawMessage(`{"ok":true}`)))
	if _, err := conn.Call(context.Background(), "tools/call", json.RawMessage(modernContinuationCall)); err != nil {
		t.Fatalf("Call: %v", err)
	}
	call := <-seen
	assertRequestMeta(t, call.Params, ModernVersions[0], true)
	kept := []string{
		`"inputResponses":{"r1":{"accepted":true}}`,
		`"requestState":9007199254740993`,
		`"custom":1`,
		`"unknown":true`,
	}
	for _, raw := range kept {
		if !strings.Contains(string(call.Params), raw) {
			t.Errorf("modern call params %s do not contain %s", call.Params, raw)
		}
	}
}

func TestStdioDiscoveryGenericRPCErrorFallsBackToLegacy(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	seen := scriptStdioUpstream(serverW, serverR,
		replyError(serverW, CodeMethodNotFound, nil),
		replyResult(serverW, InitializeResult{ProtocolVersion: LatestLegacyVersion}),
		noReply,
	)
	if _, err := conn.negotiate(context.Background()); err != nil {
		t.Fatalf("negotiate legacy server: %v", err)
	}
	want := []string{"server/discover", "initialize", NotificationInitialized}
	if got := receiveMethods(seen, len(want)); !slices.Equal(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
}

func TestStdioDiscoveryRejectsValidDisjointModernVersions(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	scriptStdioUpstream(serverW, serverR, replyResult(serverW, DiscoverResult{SupportedVersions: LegacyVersions}))
	_, err := conn.negotiate(context.Background())
	if err == nil || !strings.Contains(err.Error(), ModernVersions[0]) ||
		!strings.Contains(err.Error(), strings.Join(LegacyVersions, " ")) {
		t.Fatalf("negotiate error = %v, want requested and supported versions", err)
	}
	if conn.isModern() {
		t.Fatal("disjoint version list selected modern mode")
	}
}

func TestStdioDiscoveryDoesNotReplaceChildAfterExplicitIncompatibility(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	result := make(chan error, 1)
	go func() {
		_, err := conn.negotiate(context.Background())
		result <- err
	}()
	probe := readStdioRequest(t, serverR)
	sendResponse(serverW, probe.ID, DiscoverResult{SupportedVersions: LegacyVersions})
	serverW.Close()
	<-conn.done
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "does not support requested modern protocol") {
		t.Fatalf("negotiate error = %v, want explicit modern incompatibility", err)
	}
	if got := conn.nextID.Load(); got != 1 {
		t.Fatalf("requests sent = %d, want no legacy initialization after incompatibility", got)
	}
}

func TestStdioDiscoveryRetriesSupportedVersionErrorOnce(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	supported := json.RawMessage(`{"requested":"2026-07-28","supported":["2026-07-28"]}`)
	seen := scriptStdioUpstream(serverW, serverR,
		replyError(serverW, CodeUnsupportedProtocolVersion, supported),
		replyResult(serverW, DiscoverResult{SupportedVersions: ModernVersions}),
	)
	if _, err := conn.negotiate(context.Background()); err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	want := []string{"server/discover", "server/discover"}
	if got := receiveMethods(seen, len(want)); !slices.Equal(got, want) || !conn.isModern() {
		t.Fatalf("requests = %v, modern = %v; want one corrected discovery and modern mode", got, conn.isModern())
	}
}

func TestStdioDiscoveryTimeoutFallsBackToLegacy(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	fakeClock := clock.NewFake()
	conn.clock = fakeClock
	go io.Copy(io.Discard, serverR) //nolint:errcheck // the silent server only drains the probe.
	result := make(chan probeVerdict, 1)
	go func() {
		verdict, err := conn.discover(context.Background())
		if err != nil {
			verdict = probeVerdict{}
		}
		result <- verdict
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fakeClock.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(stdioDiscoveryTimeout)
	if verdict := <-result; verdict.era != EraLegacy {
		t.Fatalf("era after the probe timed out = %v, want legacy", verdict.era)
	}
}

func TestStdioDiscoveryParentCancellationIsTerminal(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	go io.Copy(io.Discard, serverR) //nolint:errcheck // the silent server only drains the probe.
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := conn.discover(ctx)
		result <- err
	}()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery error = %v, want context.Canceled", err)
	}
}

func TestStdioModernCancellationTargetsSentRequest(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	conn.setModern(ModernVersions[0])
	scanner := bufio.NewScanner(serverR)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := conn.Call(ctx, "tools/call", json.RawMessage(`{"name":"slow","arguments":{}}`))
		result <- err
	}()
	call := readScannerRequest(t, scanner)
	cancel()
	notice := readScannerRequest(t, scanner)
	var cancelled CancelledParams
	if notice.Method != NotificationCancelled || json.Unmarshal(notice.Params, &cancelled) != nil ||
		!sameJSONID(cancelled.RequestID, call.ID) {
		t.Fatalf(
			"after cancelling call %v mini sent %q %s, want a cancel for that call",
			call.ID,
			notice.Method,
			notice.Params,
		)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call error = %v, want context.Canceled", err)
	}
}

// scriptStdioUpstream answers the connection's requests in order, one reply per request, and
// passes each request it read to the returned channel.
func scriptStdioUpstream(serverW io.Writer, serverR io.Reader, replies ...func(Request)) <-chan Request {
	seen := make(chan Request, len(replies))
	scanner := bufio.NewScanner(serverR)
	go func() {
		for _, reply := range replies {
			var req Request
			if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &req) != nil {
				return
			}
			seen <- req
			reply(req)
		}
	}()
	return seen
}

func replyResult(serverW io.Writer, result any) func(Request) {
	return func(req Request) { sendResponse(serverW, req.ID, result) }
}

func replyError(serverW io.Writer, code int, data json.RawMessage) func(Request) {
	return func(req Request) { writeRPCError(serverW, req.ID, code, "rejected", data) }
}

func noReply(Request) {}

func receiveMethods(seen <-chan Request, n int) []string {
	methods := make([]string, 0, n)
	for range n {
		methods = append(methods, (<-seen).Method)
	}
	return methods
}

func assertRequestMeta(t *testing.T, params json.RawMessage, protocolVersion string, elicitation bool) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		t.Fatal(err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(fields["_meta"], &meta); err != nil {
		t.Fatalf("request _meta = %s: %v", fields["_meta"], err)
	}
	var got RequestMeta
	if err := json.Unmarshal(fields["_meta"], &got); err != nil {
		t.Fatal(err)
	}
	if got.ProtocolVersion != protocolVersion || got.ClientInfo == nil || got.ClientInfo.Name != "mini" ||
		got.ClientCapabilities == nil {
		t.Fatalf("request metadata = %+v", got)
	}
	if !elicitation && string(got.ClientCapabilities.Elicitation) != "" {
		t.Fatalf("discovery unexpectedly advertises elicitation: %s", fields["_meta"])
	}
	if elicitation && !strings.Contains(string(meta[clientCapabilitiesMeta]), "elicitation") {
		t.Fatalf("call metadata lost elicitation: %s", meta[clientCapabilitiesMeta])
	}
}

func writeRPCError(w io.Writer, id any, code int, message string, data json.RawMessage) {
	errorBody, _ := json.Marshal(RPCError{Code: code, Message: message, Data: data})
	line, _ := json.Marshal(map[string]any{"jsonrpc": JsonRPCVersion, "id": id, "error": json.RawMessage(errorBody)})
	fmt.Fprintf(w, "%s\n", line)
}

func readScannerRequest(t *testing.T, scanner *bufio.Scanner) Request {
	t.Helper()
	if !scanner.Scan() {
		t.Fatal("missing stdio request")
	}
	var request Request
	if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	return request
}
