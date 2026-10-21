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
	assertRequestMeta(t, requestMetaParams{
		Params:          discover.Params,
		ProtocolVersion: ModernVersions[0],
		Elicitation:     false,
	})
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
	assertRequestMeta(t, requestMetaParams{
		Params:          call.Params,
		ProtocolVersion: ModernVersions[0],
		Elicitation:     true,
	})
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

func TestStdioDiscoveryFallsBackToLegacy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply func(io.Writer) func(Request)
	}{
		{name: "generic RPC error", reply: func(w io.Writer) func(Request) {
			return replyError(w, CodeMethodNotFound, nil)
		}},
		{name: "result without supported versions", reply: func(w io.Writer) func(Request) {
			return replyResult(w, json.RawMessage(`{}`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			seen := scriptStdioUpstream(serverW, serverR,
				tc.reply(serverW),
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
		})
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

func TestStdioOnlyAnUnexplainedExitBeforeSetupGetsAReplacement(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	explicit := explicitModernProbeError{errors.New("modern discovery rejected")}
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		err    error
		exited bool
		want   bool
	}{
		{name: "exit after a generic error", ctx: context.Background(), err: errors.New("closed"), exited: true, want: true},
		{name: "exit after an explicit rejection", ctx: context.Background(), err: explicit, exited: true},
		{name: "failure while still running", ctx: context.Background(), err: errors.New("bad result")},
		{name: "caller cancelled", ctx: cancelled, err: context.Canceled, exited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := newWriteOnlyConn(discardWriteCloser{Writer: io.Discard})
			if tc.exited {
				conn.closeDone()
			}
			if got := conn.exitedBeforeSetup(tc.ctx, tc.err); got != tc.want {
				t.Fatalf("exitedBeforeSetup = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStdioDiscoveryExplicitRejectionFailsWithoutInitialize(t *testing.T) {
	noMutualVersion := json.RawMessage(`{"requested":"2026-07-28","supported":["2025-06-18"]}`)
	for _, tc := range []struct {
		name string
		code int
		data json.RawMessage
		want string
	}{
		{name: "header mismatch", code: CodeHeaderMismatch, want: "modern discovery rejected"},
		{name: "missing client capability", code: CodeMissingRequiredClientCapability, want: "modern discovery rejected"},
		{
			name: "no mutual version", code: CodeUnsupportedProtocolVersion, data: noMutualVersion,
			want: "does not support requested modern protocol",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			scriptStdioUpstream(serverW, serverR, replyError(serverW, tc.code, tc.data), noReply)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.negotiate(ctx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("negotiate error = %v, want %q and no legacy initialize", err, tc.want)
			}
		})
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

func TestStdioDiscoveryMalformedRetryNeverFallsBack(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	supported := json.RawMessage(`{"requested":"2026-07-28","supported":["2026-07-28"]}`)
	scriptStdioUpstream(serverW, serverR,
		replyError(serverW, CodeUnsupportedProtocolVersion, supported),
		replyResult(serverW, json.RawMessage(`{}`)),
		noReply,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := conn.negotiate(ctx)
	if err == nil || !strings.Contains(err.Error(), "returned no supported versions") {
		t.Fatalf("negotiate error = %v, want the malformed retry reported and no legacy initialize", err)
	}
}

func TestStdioDiscoveryTimeoutIsUnanswered(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	fakeClock := clock.NewFake()
	conn.clock = fakeClock
	go io.Copy(io.Discard, serverR)
	type outcome struct {
		verdict probeVerdict
		err     error
	}
	result := make(chan outcome, 1)
	go func() {
		verdict, err := conn.discover(context.Background())
		result <- outcome{verdict, err}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fakeClock.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(stdioDiscoveryTimeout)
	if got := <-result; !errors.Is(got.err, errProbeUnanswered) {
		t.Fatalf("discover after the probe timed out = %v, want errProbeUnanswered", got.err)
	}
}

func TestStdioDiscoveryParentCancellationIsTerminal(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	go io.Copy(io.Discard, serverR)
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
	return func(req Request) {
		writeRPCError(rpcErrorParams{
			Writer:  serverW,
			ID:      req.ID,
			Code:    code,
			Message: "rejected",
			Data:    data,
		})
	}
}

func noReply(Request) {}

func receiveMethods(seen <-chan Request, n int) []string {
	methods := make([]string, 0, n)
	for range n {
		methods = append(methods, (<-seen).Method)
	}
	return methods
}

type requestMetaParams struct {
	Params          json.RawMessage
	ProtocolVersion string
	Elicitation     bool
}

func assertRequestMeta(t *testing.T, p requestMetaParams) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(p.Params, &fields); err != nil {
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
	if got.ProtocolVersion != p.ProtocolVersion || got.ClientInfo == nil || got.ClientInfo.Name != "mini" ||
		got.ClientCapabilities == nil {
		t.Fatalf("request metadata = %+v", got)
	}
	if !p.Elicitation && string(got.ClientCapabilities.Elicitation) != "" {
		t.Fatalf("discovery unexpectedly advertises elicitation: %s", fields["_meta"])
	}
	if p.Elicitation && !strings.Contains(string(meta[clientCapabilitiesMeta]), "elicitation") {
		t.Fatalf("call metadata lost elicitation: %s", meta[clientCapabilitiesMeta])
	}
}

type rpcErrorParams struct {
	Writer  io.Writer
	ID      any
	Code    int
	Message string
	Data    json.RawMessage
}

func writeRPCError(p rpcErrorParams) {
	errorBody, _ := json.Marshal(RPCError{Code: p.Code, Message: p.Message, Data: p.Data})
	line, _ := json.Marshal(map[string]any{"jsonrpc": JsonRPCVersion, "id": p.ID, "error": json.RawMessage(errorBody)})
	fmt.Fprintf(p.Writer, "%s\n", line)
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

func TestStdioCancellationGivesUpOnItsTurnToWrite(t *testing.T) {
	conn := newWriteOnlyConn(discardWriteCloser{Writer: io.Discard})
	fakeClock := clock.NewFake()
	conn.clock = fakeClock
	if err := conn.lockWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer conn.unlockWrite()
	sent := make(chan struct{})
	go func() {
		conn.cancelUpstreamRequest(context.Background(), 1)
		close(sent)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fakeClock.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(stdioCancelWriteTimeout)
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("the cancellation kept waiting behind another write")
	}
	if conn.Health(context.Background()) != nil {
		t.Fatal("a cancellation that never started writing ended the connection")
	}
}
