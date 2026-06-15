package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	var port int
	fmt.Sscanf(u.Port(), "%d", &port)
	return port
}

func closedPort(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	port := serverPort(t, srv)
	srv.Close()
	return port
}

func testConn(port int, sessionID string) daemonConn {
	return daemonConn{client: &http.Client{}, port: port, sessionID: sessionID}
}

func rpcRequest(id int, method string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"%s"}`, id, method)
}

func rpcResult(id int, result string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, id, result)
}

func rpcOK(w http.ResponseWriter) {
	fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
}

func rpcError(w http.ResponseWriter, id, code int, msg string) {
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":%d,"message":"%s"}}`, id, code, msg)
}

func requireBearer(w http.ResponseWriter, r *http.Request, token string) bool {
	if r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func startTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func startOKServer(t *testing.T) *httptest.Server {
	t.Helper()
	return startTestServer(t, func(w http.ResponseWriter, _ *http.Request) { rpcOK(w) })
}

func forwardTo(t *testing.T, srv *httptest.Server, body []byte) ([]byte, int) {
	t.Helper()
	return forward(testConn(serverPort(t, srv), "sess"), body)
}

func mustRunProxy(t *testing.T, p RunParams) string {
	t.Helper()
	var out strings.Builder
	p.Out = &out
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	return out.String()
}

func TestForward_sendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		rpcOK(w)
	})
	conn := daemonConn{client: &http.Client{}, port: serverPort(t, srv), sessionID: "sess", token: "secret-token"}
	forward(conn, []byte(rpcRequest(1, "test")))
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}
}

func TestForward_noTokenOmitsAuthorizationHeader(t *testing.T) {
	var hadAuth bool
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		rpcOK(w)
	})
	forwardTo(t, srv, []byte(rpcRequest(1, "test")))
	if hadAuth {
		t.Error("expected no Authorization header when token is empty")
	}
}

func TestRun_propagatesTokenThroughRunParams(t *testing.T) {
	var gotAuth string
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		rpcOK(w)
	})
	mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess", Token: "tok-42",
		In: strings.NewReader(rpcRequest(1, "tools/call") + "\n"),
	})
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
	srv := startOKServer(t)
	resp, _ := forwardTo(t, srv, []byte(rpcRequest(1, "tools/call")))
	if !strings.Contains(string(resp), `"result":"ok"`) {
		t.Errorf("unexpected response: %s", resp)
	}
}

func TestForward_daemonUnreachableReturnsErrorEnvelope(t *testing.T) {
	resp, _ := forward(testConn(closedPort(t), "sess"), []byte(rpcRequest(1, "tools/call")))
	if resp == nil || !strings.Contains(string(resp), `"error"`) {
		t.Errorf("expected error envelope, got %s", resp)
	}
}

func TestForward_httpErrorStatusReturnsJSONRPCError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, "unauthorized"},
		{"forbidden", http.StatusForbidden, "forbidden: loopback only"},
		{"badRequest", http.StatusBadRequest, "read error"},
		{"internalServerError", http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			resp, _ := forwardTo(t, srv, []byte(rpcRequest(7, "tools/call")))
			var rpc struct {
				ID    json.RawMessage `json:"id"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp, &rpc); err != nil {
				t.Fatalf("response is not valid JSON-RPC: %v\ngot: %s", err, resp)
			}
			if string(rpc.ID) != "7" {
				t.Errorf("id = %s, want 7", rpc.ID)
			}
			if rpc.Error == nil {
				t.Fatalf("expected error object, got: %s", resp)
			}
		})
	}
}

func TestForward_202NotificationReturnsNil(t *testing.T) {
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	resp, _ := forwardTo(t, srv, []byte(rpcRequest(1, "notifications/initialized")))
	if resp != nil {
		t.Errorf("expected nil for 202 Accepted, got %s", resp)
	}
}

func TestForward_sessionIdPropagatedInHeader(t *testing.T) {
	var gotSession string
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("Mcp-Session-Id")
		rpcOK(w)
	})
	forward(testConn(serverPort(t, srv), "my-session-42"), []byte(rpcRequest(1, "test")))
	if gotSession != "my-session-42" {
		t.Errorf("session header = %q, want %q", gotSession, "my-session-42")
	}
}

func TestForward_largeResponseBodyHandledWithoutError(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, big) })
	resp, _ := forwardTo(t, srv, []byte(rpcRequest(1, "test")))
	if len(resp) == 0 {
		t.Error("expected non-empty response for large body")
	}
}

func runParams(t *testing.T, srv *httptest.Server, in io.Reader, out io.Writer) RunParams {
	t.Helper()
	return RunParams{Port: serverPort(t, srv), SessionID: "sess", In: in, Out: out}
}

func TestRun_routesRequestAndWritesResponse(t *testing.T) {
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, rpcResult(1, `"done"`))
	})
	got := mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess",
		In: strings.NewReader(rpcRequest(1, "tools/call") + "\n"),
	})
	if !strings.Contains(got, "done") {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestRun_emptyLinesSkipped(t *testing.T) {
	var calls atomic.Int32
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		rpcOK(w)
	})
	in := strings.NewReader("\n\n" + rpcRequest(1, "tools/call") + "\n\n")
	Run(runParams(t, srv, in, io.Discard)) //nolint:errcheck
	if calls.Load() != 1 {
		t.Errorf("expected 1 daemon call (empty lines skipped), got %d", calls.Load())
	}
}

func TestRun_cleanEOFReturnsNilError(t *testing.T) {
	srv := startOKServer(t)
	if err := Run(runParams(t, srv, strings.NewReader(""), io.Discard)); err != nil {
		t.Errorf("expected nil error on clean EOF, got %v", err)
	}
}

func TestRun_multipleRequestsAllForwarded(t *testing.T) {
	var calls atomic.Int32
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		fmt.Fprint(w, rpcResult(int(n), `"ok"`))
	})
	in := strings.NewReader(rpcRequest(1, "a") + "\n" + rpcRequest(2, "b") + "\n")
	if err := Run(runParams(t, srv, in, io.Discard)); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 daemon calls, got %d", calls.Load())
	}
}

func TestInjectProxyMode_initialize_addsFlag(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
	got := injectProxyMode(line)
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(got, &msg); err != nil {
		t.Fatalf("unmarshal result: %v\ngot: %s", err, got)
	}
	if string(msg.Params["_mini_proxy_mode"]) != "true" {
		t.Errorf("_mini_proxy_mode = %s, want true", msg.Params["_mini_proxy_mode"])
	}
}

func TestInjectProxyMode_initialize_preservesExistingParams(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test"}}}`)
	got := injectProxyMode(line)
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

func TestInjectProxyMode_nonInitialize_unchanged(t *testing.T) {
	cases := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"foo"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}
	for _, line := range cases {
		got := injectProxyMode([]byte(line))
		if string(got) != line {
			t.Errorf("non-initialize message modified:\nwant: %s\n got: %s", line, got)
		}
	}
}

func TestInjectProxyMode_malformedJSON_unchanged(t *testing.T) {
	line := []byte(`not valid json`)
	got := injectProxyMode(line)
	if string(got) != string(line) {
		t.Errorf("malformed JSON should be returned unchanged, got: %s", got)
	}
}

func TestInjectProxyMode_initialize_noParams_addsFlag(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	got := injectProxyMode(line)
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(got, &msg); err != nil {
		t.Fatalf("unmarshal result: %v\ngot: %s", err, got)
	}
	if string(msg.Params["_mini_proxy_mode"]) != "true" {
		t.Errorf("_mini_proxy_mode = %s, want true", msg.Params["_mini_proxy_mode"])
	}
}

func TestRun_proxyMode_injectsIntoInitialize(t *testing.T) {
	var gotBody []byte
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, rpcResult(1, `{}`))
	})
	mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess", ProxyMode: true,
		In: strings.NewReader(rpcRequest(1, "initialize") + "\n"),
	})
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(gotBody, &msg); err != nil {
		t.Fatalf("unmarshal forwarded body: %v\nbody: %s", err, gotBody)
	}
	if string(msg.Params["_mini_proxy_mode"]) != "true" {
		t.Errorf("daemon did not receive _mini_proxy_mode=true; params: %v", msg.Params)
	}
}

func TestRun_standardMode_doesNotInjectFlag(t *testing.T) {
	var gotBody []byte
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, rpcResult(1, `{}`))
	})
	mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess",
		In: strings.NewReader(rpcRequest(1, "initialize") + "\n"),
	})
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(gotBody, &msg); err != nil {
		t.Fatalf("unmarshal forwarded body: %v", err)
	}
	if msg.Params["_mini_proxy_mode"] != nil {
		t.Errorf("standard mode should not inject _mini_proxy_mode; params: %v", msg.Params)
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
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch methodOf(t, r) {
		case "initialize":
			initCalls.Add(1)
			fmt.Fprint(w, rpcResult(-1, `{"protocolVersion":"2025-03-26"}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			if n := toolCalls.Add(1); n == 1 {
				rpcError(w, 1, -32600, "not initialized: send initialize first")
			} else {
				fmt.Fprint(w, rpcResult(1, `"recovered"`))
			}
		}
	})
	output := mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess",
		In: strings.NewReader(rpcRequest(1, "tools/call") + "\n"),
	})
	if got := initCalls.Load(); got != 1 {
		t.Errorf("initialize calls during reinit = %d, want 1", got)
	}
	if got := toolCalls.Load(); got != 2 {
		t.Errorf("tool calls (original + retry) = %d, want 2", got)
	}
	if !strings.Contains(output, "recovered") {
		t.Errorf("expected recovered response in output, got: %q", output)
	}
}

func TestRun_noReinitWhenInitializeSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		rpcOK(w)
	})
	mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess",
		In: strings.NewReader(rpcRequest(1, "initialize") + "\n"),
	})
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call for initialize, got %d", got)
	}
}

func newConcurrencyServer(t *testing.T, release chan struct{}) (*httptest.Server, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	var current, maxSeen atomic.Int32
	var once sync.Once
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		rpcOK(w)
	}))
	t.Cleanup(srv.Close)
	return srv, &maxSeen, started
}

func TestRunWithLimit_capsConcurrentForwards(t *testing.T) {
	release := make(chan struct{})
	srv, maxSeen, started := newConcurrencyServer(t, release)
	done := make(chan error, 1)
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"c"}` + "\n")
	go func() {
		done <- runWithLimit(RunParams{Port: serverPort(t, srv), SessionID: "sess", In: input, Out: io.Discard}, 1)
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

func methodOf(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var msg struct {
		Method string `json:"method"`
	}
	json.Unmarshal(body, &msg) //nolint:errcheck
	return msg.Method
}

func TestRun_refreshesTokenAfterUnauthorized(t *testing.T) {
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r, "newtoken") {
			return
		}
		rpcOK(w)
	})
	got := mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess", Token: "stale",
		ReloadToken: func() (string, error) { return "newtoken", nil },
		In:          strings.NewReader(rpcRequest(1, "tools/call") + "\n"),
	})
	if !strings.Contains(got, `"result":"ok"`) {
		t.Fatalf("expected recovery after token refresh, got %q", got)
	}
}

// Recovery must walk 401 → refresh → "not initialized" → reinit → success.
func TestRun_recoversFromFullDaemonRestart(t *testing.T) {
	var initialized atomic.Bool
	srv := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r, "rotated") {
			return
		}
		switch methodOf(t, r) {
		case "initialize":
			initialized.Store(true)
			fmt.Fprint(w, rpcResult(-1, `{}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			if !initialized.Load() {
				rpcError(w, 1, -32002, "not initialized")
				return
			}
			rpcOK(w)
		}
	})
	got := mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess", Token: "stale",
		ReloadToken: func() (string, error) { return "rotated", nil },
		In:          strings.NewReader(rpcRequest(1, "tools/call") + "\n"),
	})
	if !strings.Contains(got, `"result":"ok"`) {
		t.Fatalf("expected full restart recovery, got %q", got)
	}
}

func TestRun_persistentUnauthorizedReturnsErrorEnvelope(t *testing.T) {
	var hits atomic.Int64
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	got := mustRunProxy(t, RunParams{
		Port: serverPort(t, srv), SessionID: "sess", Token: "stale",
		ReloadToken: func() (string, error) { return "still-stale", nil },
		In:          strings.NewReader(rpcRequest(7, "tools/call") + "\n"),
	})
	if !strings.Contains(got, `"error"`) || !strings.Contains(got, `"id":7`) {
		t.Fatalf("expected JSON-RPC error envelope, got %q", got)
	}
	if strings.TrimSpace(got) == "unauthorized" {
		t.Fatal("raw 401 body leaked to agent")
	}
	if hits.Load() != 2 {
		t.Errorf("expected one refresh retry (2 hits), got %d", hits.Load())
	}
}

