//go:build test

package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
)

type responseReadFailure struct {
	data []byte
	read atomic.Bool
}

func (r *responseReadFailure) Read(p []byte) (int, error) {
	r.read.Store(true)
	if len(r.data) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, io.ErrUnexpectedEOF
}

func (*responseReadFailure) Close() error { return nil }

type responseRoundTripper struct {
	status int
	body   io.ReadCloser
	calls  atomic.Int32
}

func (r *responseRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return &http.Response{
		StatusCode: r.status,
		Header:     make(http.Header),
		Body:       r.body,
	}, nil
}

func TestProxySession_responseReadFailureReturnsErrorWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		kind   forwardedMessageKind
		data   string
	}{
		{name: "initialize/empty", method: "initialize", kind: forwardedMessageInitialize},
		{name: "initialize/partial", method: "initialize", kind: forwardedMessageInitialize, data: `{"jsonrpc":"2.0","id":91,"result":`},
		{name: "tool-call/empty", method: "tools/call", kind: forwardedMessageOther},
		{name: "tool-call/partial", method: "tools/call", kind: forwardedMessageOther, data: `{"jsonrpc":"2.0","id":91,"result":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readFailure := &responseReadFailure{data: []byte(tc.data)}
			roundTripper := &responseRoundTripper{status: http.StatusOK, body: readFailure}
			client := &http.Client{Transport: roundTripper}
			var resolves atomic.Int32
			session := newProxySession(RunParams{
				Client: client,
				Resolver: NewDaemonResolver(func() (string, error) {
					resolves.Add(1)
					return "", errors.New("unexpected recovery")
				}),
				SessionID: "session",
				Clock:     clock.NewFake(),
			}, newLineWriter(io.Discard))

			request := []byte(`{"jsonrpc":"2.0","id":91,"method":"` + tc.method + `"}`)
			response := session.forward(forwardedMessage{line: request, kind: tc.kind})
			t.Cleanup(session.close)

			if !jsonRPCErrorHasID(response, "91") || !strings.Contains(string(response), "unexpected EOF") {
				t.Errorf("response = %s, want a JSON-RPC error preserving id 91", response)
			}
			if got := roundTripper.calls.Load(); got != 1 {
				t.Errorf("request attempts = %d, want 1", got)
			}
			if got := resolves.Load(); got != 0 {
				t.Errorf("resolver calls = %d, want 0", got)
			}
			if !readFailure.read.Load() {
				t.Error("successful-status response body was not read")
			}
			if tc.kind == forwardedMessageInitialize {
				if session.initialized.Load() {
					t.Error("failed initialize marked session initialized")
				}
				if session.clientReady.Load() {
					t.Error("failed initialize marked client ready")
				}
			}
		})
	}
}

func TestProxySession_acceptedNotificationDoesNotReadBody(t *testing.T) {
	body := &responseReadFailure{}
	roundTripper := &responseRoundTripper{status: http.StatusAccepted, body: body}
	session := newProxySession(RunParams{
		Client:    &http.Client{Transport: roundTripper},
		SessionID: "session",
		Clock:     clock.NewFake(),
	}, newLineWriter(io.Discard))
	t.Cleanup(session.close)

	request := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	response := session.forward(forwardedMessage{line: request, kind: forwardedMessageInitialized})
	if response != nil {
		t.Errorf("response = %s, want nil", response)
	}
	if body.read.Load() {
		t.Error("accepted notification response body was read")
	}
	if got := roundTripper.calls.Load(); got != 1 {
		t.Errorf("request attempts = %d, want 1", got)
	}
}

func jsonRPCErrorHasID(response []byte, want string) bool {
	var rpc struct {
		ID    json.RawMessage `json:"id"`
		Error json.RawMessage `json:"error"`
	}
	return json.Unmarshal(response, &rpc) == nil && string(rpc.ID) == want && len(rpc.Error) > 0
}
