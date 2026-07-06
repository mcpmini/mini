//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func shortSocketDir(t *testing.T) string { return testutil.ShortTempDir(t) }

func serveSocket(t *testing.T, h http.HandlerFunc) *http.Client {
	t.Helper()
	sock := filepath.Join(shortSocketDir(t), "d.sock")
	testutil.StartUnixServer(t, sock, h)
	return daemon.SocketClient(sock, 0)
}

func deadClient(t *testing.T) *http.Client {
	t.Helper()
	return daemon.SocketClient(filepath.Join(shortSocketDir(t), "nonexistent.sock"), 0)
}

func testConn(client *http.Client, sessionID string) DaemonSession {
	return DaemonSession{client: client, sessionID: sessionID}
}

func TestForward_sendsBearerToken(t *testing.T) {
	var gotAuth string
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	})
	conn := DaemonSession{client: client, sessionID: "sess", token: "secret-token"}
	conn.Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}
}

func TestForward_noTokenOmitsAuthorizationHeader(t *testing.T) {
	var hadAuth bool
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	})
	testConn(client, "sess").Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if hadAuth {
		t.Error("expected no Authorization header when token is empty")
	}
}

func TestRun_propagatesTokenThroughRunParams(t *testing.T) {
	var gotAuth string
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	})
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}` + "\n")
	p := RunParams{Client: client, SessionID: "sess", Token: "tok-42", In: in, Out: io.Discard, Clock: clock.NewFake()}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if gotAuth != "Bearer tok-42" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok-42")
	}
}

func TestDaemonErrorResponse_withIdReturnsEnvelope(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`)
	resp := daemonErrorResponse(body, "boom")
	if resp == nil {
		t.Fatal("expected non-nil response for request with id")
	}
	s := string(resp)
	if !strings.Contains(s, `"error"`) || !strings.Contains(s, "boom") || !strings.Contains(s, `"id":1`) {
		t.Errorf("unexpected response: %s", s)
	}
}

func TestDaemonErrorResponse_notificationReturnsNil(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp := daemonErrorResponse(body, "irrelevant"); resp != nil {
		t.Errorf("expected nil for notification (no id), got %s", resp)
	}
}

func TestDaemonErrorResponse_malformedBodyReturnsNil(t *testing.T) {
	if resp := daemonErrorResponse([]byte(`not json`), "msg"); resp != nil {
		t.Errorf("expected nil for unparseable body, got %s", resp)
	}
}

func TestForward_successReturnsDaemonResponse(t *testing.T) {
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	})
	resp := testConn(client, "sess").Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	if !strings.Contains(string(resp), `"result":"ok"`) {
		t.Errorf("unexpected response: %s", resp)
	}
}

func TestForward_daemonUnreachableReturnsErrorEnvelope(t *testing.T) {
	resp := testConn(deadClient(t), "sess").Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	if resp == nil || !strings.Contains(string(resp), `"error"`) {
		t.Errorf("expected error envelope, got %s", resp)
	}
}

func TestForward_httpErrorStatusProducesJSONRPCEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, "unauthorized"},
		{"forbidden", http.StatusForbidden, "loopback only"},
		{"badRequest", http.StatusBadRequest, "read error"},
		{"internalServerError", http.StatusInternalServerError, "oops"},
	}
	reqBody := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call"}`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			resp := testConn(client, "sess").Send(reqBody)
			var rpc struct {
				ID    json.RawMessage `json:"id"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp, &rpc); err != nil {
				t.Fatalf("response not valid JSON-RPC: %v\ngot: %s", err, resp)
			}
			if string(rpc.ID) != "7" {
				t.Errorf("id = %s, want 7", rpc.ID)
			}
			if rpc.Error == nil {
				t.Fatalf("expected JSON-RPC error object, got: %s", resp)
			}
			if strings.TrimSpace(string(resp)) == tc.body {
				t.Error("raw daemon body leaked verbatim to agent")
			}
		})
	}
}

func TestForward_202NotificationReturnsNil(t *testing.T) {
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	resp := testConn(client, "sess").Send([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if resp != nil {
		t.Errorf("expected nil for 202 Accepted, got %s", resp)
	}
}

func TestForward_sessionIdPropagatedInHeader(t *testing.T) {
	var gotSession string
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("Mcp-Session-Id")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	})
	testConn(client, "my-session-42").Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if gotSession != "my-session-42" {
		t.Errorf("session header = %q, want %q", gotSession, "my-session-42")
	}
}

func TestForward_largeResponseBodyHandledWithoutError(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, big)
	})
	resp := testConn(client, "sess").Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if len(resp) == 0 {
		t.Error("expected non-empty response for large body")
	}
}

func runParams(client *http.Client, in io.Reader, out io.Writer) RunParams {
	return RunParams{Client: client, SessionID: "sess", In: in, Out: out, Clock: clock.NewFake()}
}

func TestRun_routesRequestAndWritesResponse(t *testing.T) {
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"done"}`)
	})
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}` + "\n")
	var out bytes.Buffer
	if err := Run(runParams(client, in, &out)); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), "done") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestRun_emptyLinesSkipped(t *testing.T) {
	var calls atomic.Int32
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	})
	in := strings.NewReader("\n\n" + `{"jsonrpc":"2.0","id":1,"method":"tools/call"}` + "\n\n")
	Run(runParams(client, in, io.Discard)) //nolint:errcheck
	if calls.Load() != 1 {
		t.Errorf("expected 1 daemon call (empty lines skipped), got %d", calls.Load())
	}
}

func TestRun_cleanEOFReturnsNilError(t *testing.T) {
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {})
	if err := Run(runParams(client, strings.NewReader(""), io.Discard)); err != nil {
		t.Errorf("expected nil error on clean EOF, got %v", err)
	}
}

func TestRun_multipleRequestsAllForwarded(t *testing.T) {
	var calls atomic.Int32
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"ok"}`, n)
	})
	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n",
	)
	if err := Run(runParams(client, in, io.Discard)); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 daemon calls, got %d", calls.Load())
	}
}

func TestInjectCompactMode_initialize_addsFlag(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
	got := injectCompactMode(line)
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(got, &msg); err != nil {
		t.Fatalf("unmarshal result: %v\ngot: %s", err, got)
	}
	if string(msg.Params["_mini_tool_mode"]) != `"compact"` {
		t.Errorf("_mini_tool_mode = %s, want \"compact\"", msg.Params["_mini_tool_mode"])
	}
}

func TestInjectCompactMode_initialize_preservesExistingParams(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test"}}}`)
	got := injectCompactMode(line)
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(got, &msg); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if string(msg.Params["protocolVersion"]) != `"2025-03-26"` {
		t.Errorf("protocolVersion lost: %s", msg.Params["protocolVersion"])
	}
	if msg.Params["clientInfo"] == nil {
		t.Error("clientInfo lost after injection")
	}
}

func TestInjectCompactMode_nonInitialize_unchanged(t *testing.T) {
	cases := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"foo"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}
	for _, line := range cases {
		got := injectCompactMode([]byte(line))
		if string(got) != line {
			t.Errorf("non-initialize message modified:\nwant: %s\n got: %s", line, got)
		}
	}
}

func TestInjectCompactMode_malformedJSON_unchanged(t *testing.T) {
	line := []byte(`not valid json`)
	got := injectCompactMode(line)
	if string(got) != string(line) {
		t.Errorf("malformed JSON should be returned unchanged, got: %s", got)
	}
}

func TestInjectCompactMode_initialize_noParams_addsFlag(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	got := injectCompactMode(line)
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(got, &msg); err != nil {
		t.Fatalf("unmarshal result: %v\ngot: %s", err, got)
	}
	if string(msg.Params["_mini_tool_mode"]) != `"compact"` {
		t.Errorf("_mini_tool_mode = %s, want \"compact\"", msg.Params["_mini_tool_mode"])
	}
}

func TestRun_compact_injectsIntoInitialize(t *testing.T) {
	var gotBody []byte
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	})

	initMsg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}` + "\n"
	p := RunParams{
		Client:    client,
		SessionID: "sess",
		In:        strings.NewReader(initMsg),
		Out:       io.Discard,
		ToolMode:  transport.ToolModeCompact,
		Clock:     clock.NewFake(),
	}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(gotBody, &msg); err != nil {
		t.Fatalf("unmarshal forwarded body: %v\nbody: %s", err, gotBody)
	}
	if string(msg.Params["_mini_tool_mode"]) != `"compact"` {
		t.Errorf("daemon did not receive _mini_tool_mode=\"compact\"; params: %v", msg.Params)
	}
}

func TestRun_proxy_doesNotInjectFlag(t *testing.T) {
	var gotBody []byte
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	})

	initMsg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}` + "\n"
	p := RunParams{
		Client:    client,
		SessionID: "sess",
		In:        strings.NewReader(initMsg),
		Out:       io.Discard,
		ToolMode:  transport.ToolModeProxy,
		Clock:     clock.NewFake(),
	}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(gotBody, &msg); err != nil {
		t.Fatalf("unmarshal forwarded body: %v", err)
	}
	if msg.Params["_mini_tool_mode"] != nil {
		t.Errorf("proxy mode should not inject _mini_tool_mode; params: %v", msg.Params)
	}
}

func TestIsNotInitialized_trueForNotInitializedError(t *testing.T) {
	resp := []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"not initialized: send initialize first"}}`)
	if !isNotInitialized(resp) {
		t.Error("expected true for not-initialized error")
	}
}

func TestIsNotInitialized_falseForOtherErrors(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"internal error"}}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`),
		nil,
		{},
	}
	for _, c := range cases {
		if isNotInitialized(c) {
			t.Errorf("expected false for %q", c)
		}
	}
}

func TestPeekIsInitialize_detectsInitialize(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if !peekIsInitialize(line) {
		t.Error("expected true for initialize method")
	}
}

func TestPeekIsInitialize_falseForOtherMethods(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
		[]byte(`not json`),
	}
	for _, c := range cases {
		if peekIsInitialize(c) {
			t.Errorf("expected false for %q", c)
		}
	}
}

func TestRun_reinitsAndRetriesOnNotInitializedError(t *testing.T) {
	var toolCalls, initCalls atomic.Int32
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			Method string `json:"method"`
		}
		json.Unmarshal(body, &msg) //nolint:errcheck
		switch msg.Method {
		case "initialize":
			initCalls.Add(1)
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":-1,"result":{"protocolVersion":"2025-03-26"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			if n := toolCalls.Add(1); n == 1 {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"not initialized: send initialize first"}}`)
			} else {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"recovered"}`)
			}
		}
	})

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}` + "\n")
	var out bytes.Buffer
	p := RunParams{Client: client, SessionID: "sess", In: in, Out: &out, Clock: clock.System()}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if got := initCalls.Load(); got != 1 {
		t.Errorf("initialize calls during reinit = %d, want 1", got)
	}
	if got := toolCalls.Load(); got != 2 {
		t.Errorf("tool calls (original + retry) = %d, want 2", got)
	}
	if !strings.Contains(out.String(), "recovered") {
		t.Errorf("expected recovered response in output, got: %q", out.String())
	}
}

func TestRun_relaysDaemonNotificationsAfterInitialized(t *testing.T) {
	initLine := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	readyLine := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	getStarted := make(chan struct{}, 1)
	notifSent := make(chan struct{}, 1)
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
			getStarted <- struct{}{}
			notifSent <- struct{}{}
			return
		}
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		json.Unmarshal(body, &msg) //nolint:errcheck
		switch msg.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26"}}`, msg.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected POST method %q", msg.Method)
		}
	})

	inR, inW := io.Pipe()
	out := &safeBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{Client: client, SessionID: "sess", In: inR, Out: out, Clock: clock.System()})
	}()

	fmt.Fprintln(inW, initLine)
	fmt.Fprintln(inW, readyLine)
	select {
	case <-getStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for daemon GET stream")
	}
	select {
	case <-notifSent:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for daemon notification")
	}
	waitForOutputContains(t, out, `"method":"notifications/tools/list_changed"`)
	inW.Close()
	if err := <-done; err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), `"id":1`) {
		t.Fatalf("missing initialize response in output: %q", out.String())
	}
	if !strings.Contains(out.String(), `"method":"notifications/tools/list_changed"`) {
		t.Fatalf("missing relayed notification in output: %q", out.String())
	}
}

func TestRun_reopensNotificationStreamAfterRecovery(t *testing.T) {
	initLine := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	readyLine := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	callLine := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`
	notifSent := make(chan struct{}, 1)
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if r.Method == http.MethodGet {
			if auth != "Bearer fresh" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
			notifSent <- struct{}{}
			return
		}
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		json.Unmarshal(body, &msg) //nolint:errcheck
		switch msg.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26"}}`, msg.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			if auth != "Bearer fresh" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"ok"}`, msg.ID)
		default:
			t.Fatalf("unexpected POST method %q", msg.Method)
		}
	})

	inR, inW := io.Pipe()
	out := &safeBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{
			Client: client, SessionID: "sess", Token: "stale", In: inR, Out: out,
			Resolver: NewDaemonResolver(func() (string, error) { return "fresh", nil }),
			Clock:    clock.System(),
		})
	}()

	fmt.Fprintln(inW, initLine)
	fmt.Fprintln(inW, readyLine)
	fmt.Fprintln(inW, callLine)
	select {
	case <-notifSent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for recovered notification stream")
	}
	waitForOutputContains(t, out, `"method":"notifications/tools/list_changed"`)
	inW.Close()
	if err := <-done; err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), `"result":"ok"`) {
		t.Fatalf("missing recovered tool response: %q", out.String())
	}
	if !strings.Contains(out.String(), `"method":"notifications/tools/list_changed"`) {
		t.Fatalf("missing notification after recovery: %q", out.String())
	}
}

func waitForOutputContains(t *testing.T, out *safeBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in output: %q", want, out.String())
}

func TestRun_noReinitWhenInitializeSucceeds(t *testing.T) {
	var calls atomic.Int32
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	})

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var out bytes.Buffer
	p := RunParams{Client: client, SessionID: "sess", In: in, Out: &out}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call for initialize, got %d", got)
	}
}

func TestRun_noReinitLoopWhenInitializeReturnsNotInitialized(t *testing.T) {
	var calls atomic.Int32
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"not initialized: send initialize first"}}`)
	})

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	done := make(chan error, 1)
	go func() {
		done <- Run(RunParams{Client: client, SessionID: "sess", In: in, Out: io.Discard, Clock: clock.NewFake()})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return — reinit loop not prevented for initialize method")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call (no reinit), got %d", got)
	}
}

func newConcurrencyServer(t *testing.T, release chan struct{}) (*http.Client, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	var current, maxSeen atomic.Int32
	var once sync.Once
	started := make(chan struct{}, 1)
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		n := current.Add(1)
		for {
			prev := maxSeen.Load()
			if n <= prev || maxSeen.CompareAndSwap(prev, n) {
				break
			}
		}
		once.Do(func() { started <- struct{}{} })
		<-release
		current.Add(-1)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	})
	return client, &maxSeen, started
}

func TestRunWithLimit_capsConcurrentForwards(t *testing.T) {
	release := make(chan struct{})
	client, maxSeen, started := newConcurrencyServer(t, release)
	done := make(chan error, 1)
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"c"}` + "\n")
	go func() {
		done <- runWithLimit(RunParams{Client: client, SessionID: "sess", In: input, Out: io.Discard, Clock: clock.NewFake()}, 1)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first request to reach daemon")
	}
	if got := maxSeen.Load(); got != 1 {
		t.Fatalf("max concurrent forwards = %d, want 1", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("runWithLimit error: %v", err)
	}
	if got := maxSeen.Load(); got != 1 {
		t.Fatalf("max concurrent forwards after completion = %d, want 1", got)
	}
}
