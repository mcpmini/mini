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
		{name: "result listing only shared legacy versions", reply: func(w io.Writer) func(Request) {
			return replyResult(w, DiscoverResult{SupportedVersions: LegacyVersions})
		}},
		{name: "unsupported version naming only a shared legacy one", reply: func(w io.Writer) func(Request) {
			return replyError(w, CodeUnsupportedProtocolVersion, json.RawMessage(`{"supported":["2025-06-18"]}`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			seen := scriptStdioUpstream(serverW, serverR,
				tc.reply(serverW),
				replyResult(serverW, InitializeResult{ProtocolVersion: LatestLegacyVersion}),
				noReply,
			)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.negotiate(ctx); err != nil {
				t.Fatalf("negotiate legacy server: %v", err)
			}
			want := []string{"server/discover", "initialize", NotificationInitialized}
			if got := receiveMethodsWithinASecond(seen, len(want)); !slices.Equal(got, want) {
				t.Fatalf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestStdioDiscoveryRejectsAListSharingNoVersion(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	unknown := []string{"2099-01-01", "2098-01-01"}
	scriptStdioUpstream(serverW, serverR, replyResult(serverW, DiscoverResult{SupportedVersions: unknown}), noReply)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := conn.negotiate(ctx)
	if err == nil || !strings.Contains(err.Error(), ModernVersions[0]) ||
		!strings.Contains(err.Error(), strings.Join(unknown, " ")) {
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
	noMutualVersion := json.RawMessage(`{"requested":"2026-07-28","supported":["2099-01-01"]}`)
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

func TestStdioDiscoveryRefusalsAreExplicit(t *testing.T) {
	conn := newWriteOnlyConn(discardWriteCloser{Writer: io.Discard})
	rejected := func(code int) func() error {
		return func() error {
			_, _, err := conn.classifyDiscoveryRPCError(t.Context(), &RPCError{Code: code}, ModernVersions[0])
			return err
		}
	}
	refusals := map[string]func() error{
		"header mismatch":   rejected(CodeHeaderMismatch),
		"no shared version": rejected(CodeUnsupportedProtocolVersion),
		"result sharing no version": func() error {
			_, err := classifyStdioDiscover(json.RawMessage(`{"supportedVersions":["2099-01-01"]}`), ModernVersions[0])
			return err
		},
	}
	for name, refuse := range refusals {
		t.Run(name, func(t *testing.T) {
			var explicit explicitModernProbeError
			if err := refuse(); !errors.As(err, &explicit) {
				t.Fatalf("error = %v, want an explicit modern refusal", err)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.negotiate(ctx); err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	want := []string{"server/discover", "server/discover"}
	if got := receiveMethodsWithinASecond(seen, len(want)); !slices.Equal(got, want) || !conn.isModern() {
		t.Fatalf("requests = %v, modern = %v; want one corrected discovery and modern mode", got, conn.isModern())
	}
}

func TestStdioDiscoveryRetryWithoutAModernVersionNeverFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		retry json.RawMessage
		want  string
	}{
		{name: "malformed retry", retry: json.RawMessage(`{}`), want: "returned no supported versions"},
		{
			name:  "retry listing only legacy versions",
			retry: json.RawMessage(`{"supportedVersions":["2025-11-25"]}`),
			want:  `does not support requested modern protocol "2026-07-28" (supported: [2025-11-25])`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			supported := json.RawMessage(`{"requested":"2026-07-28","supported":["2026-07-28"]}`)
			scriptStdioUpstream(serverW, serverR,
				replyError(serverW, CodeUnsupportedProtocolVersion, supported),
				replyResult(serverW, tc.retry),
				noReply,
			)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.negotiate(ctx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("negotiate error = %v, want %q and no legacy initialize", err, tc.want)
			}
		})
	}
}

func TestStdioDiscoveryTimeoutIsUnanswered(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	fakeClock := clock.NewFake()
	conn.clock = fakeClock
	go io.Copy(io.Discard, serverR)
	result := make(chan error, 1)
	go func() {
		_, err := conn.discover(t.Context())
		result <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fakeClock.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(stdioDiscoveryTimeout)
	if got := <-result; !errors.Is(got, errProbeUnanswered) {
		t.Fatalf("discover after the probe timed out = %v, want errProbeUnanswered", got)
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

func receiveMethodsWithinASecond(seen <-chan Request, n int) []string {
	methods := make([]string, 0, n)
	for range n {
		select {
		case request := <-seen:
			methods = append(methods, request.Method)
		case <-time.After(time.Second):
			return methods
		}
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

func TestInitialize_respectsDeadlineWhenUpstreamNeverResponds(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	go io.Copy(io.Discard, serverR)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := conn.initialize(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected initialize to fail when upstream never responds")
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("initialize did not respect deadline, took %v", elapsed)
	}
}

func TestStdioInitialize_offersNewestLegacyAndRejectsUnsupportedVersion(t *testing.T) {
	request, err := initializeStdioForTest(t, "2025-06-18")
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var params InitializeParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("decode initialize params: %v", err)
	}
	if params.ProtocolVersion != LegacyVersions[0] {
		t.Errorf("offered protocolVersion = %q, want %q", params.ProtocolVersion, LegacyVersions[0])
	}
	for _, version := range []string{"2026-07-28", "1999-01-01"} {
		_, err := initializeStdioForTest(t, version)
		want := fmt.Sprintf("upstream negotiated unsupported protocol version %q", version)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "parse initialize") {
			t.Errorf("initialize error = %v, want %q without a parse error", err, want)
		}
	}
}

func initializeStdioForTest(t *testing.T, replyVersion string) (Request, error) {
	t.Helper()
	conn, serverW, serverR := makePipeConn(t)
	done := make(chan error, 1)
	go func() { done <- conn.initialize(context.Background()) }()
	request := readStdioRequest(t, serverR)
	sendResponse(serverW, request.ID, InitializeResult{ProtocolVersion: replyVersion})
	if IsLegacyVersion(replyVersion) {
		readStdioRequest(t, serverR)
	}
	return request, <-done
}

func readStdioRequest(t *testing.T, r io.Reader) Request {
	t.Helper()
	line, err := bufio.NewReader(r).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read stdio request: %v", err)
	}
	var request Request
	if err := json.Unmarshal(line, &request); err != nil {
		t.Fatalf("decode stdio request: %v", err)
	}
	return request
}
