package mcptest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/transport"
)

type rpcReply struct {
	Status int
	Header http.Header
	Body   string
}

func post(t *testing.T, url, body string) rpcReply {
	t.Helper()
	resp, err := http.Post(
		url,
		"application/json",
		strings.NewReader(body),
	) //nolint:noctx // the fake serves locally and returns at once
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // read fully below; nothing to report on close
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return rpcReply{Status: resp.StatusCode, Header: resp.Header, Body: string(raw)}
}

func call(method string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"` + method + `"}`
}

func TestLatestLegacyVersionMatchesTransport(t *testing.T) {
	if mcptest.LatestLegacyVersion != transport.LatestLegacyVersion {
		t.Errorf("mcptest answers %q, mini offers %q; update mcptest.LatestLegacyVersion",
			mcptest.LatestLegacyVersion, transport.LatestLegacyVersion)
	}
}

func TestNewHTTP_answersTheLegacyHandshake(t *testing.T) {
	up := mcptest.NewHTTP(t, mcptest.Upstream{Tools: []mcptest.Tool{{Name: "ping"}}})

	initialize := post(t, up.URL, call("initialize"))
	tools := post(t, up.URL, call("tools/list"))

	if !strings.Contains(initialize.Body, `"protocolVersion":"`+mcptest.LatestLegacyVersion+`"`) {
		t.Errorf("initialize = %s, want protocolVersion %s", initialize.Body, mcptest.LatestLegacyVersion)
	}
	if want := `"tools":[{"name":"ping","inputSchema":{"type":"object"}}]`; !strings.Contains(tools.Body, want) {
		t.Errorf("tools/list = %s, want %s", tools.Body, want)
	}
}

func TestNewHTTP_unknownMethodIsMethodNotFound(t *testing.T) {
	up := mcptest.NewHTTP(t, mcptest.Upstream{})

	got := post(t, up.URL, call("server/discover"))

	var resp struct {
		Error struct{ Code int } `json:"error"`
	}
	if err := json.Unmarshal([]byte(got.Body), &resp); err != nil || resp.Error.Code != -32601 {
		t.Errorf("server/discover = %s, want JSON-RPC error -32601 so mini treats the fake as legacy", got.Body)
	}
}

func TestNewHTTP_notificationIsAcceptedAndRecorded(t *testing.T) {
	up := mcptest.NewHTTP(t, mcptest.Upstream{})

	got := post(t, up.URL, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	if got.Status != http.StatusAccepted || got.Body != "" {
		t.Errorf("notification = %d %q, want 202 with no body", got.Status, got.Body)
	}
	if reqs := up.Requests(); len(reqs) != 1 || reqs[0].Method != "notifications/initialized" {
		t.Errorf("requests = %+v, want the notification", reqs)
	}
}

func TestNewHTTP_rejectedRequestIsChallengedAndStillRecorded(t *testing.T) {
	up := mcptest.NewHTTP(t, mcptest.Upstream{
		Authorize: func(r mcptest.Request) bool { return r.Header.Get("Authorization") == "Bearer ok" },
	})

	got := post(t, up.URL, call("initialize"))

	if got.Status != http.StatusUnauthorized || got.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("unauthorized = %d, WWW-Authenticate %q; want 401 with a Bearer challenge",
			got.Status, got.Header.Get("WWW-Authenticate"))
	}
	if len(up.Requests()) != 1 {
		t.Errorf("requests = %d, want the rejected one recorded", len(up.Requests()))
	}
}

func TestNewHTTP_methodOverrides(t *testing.T) {
	up := mcptest.NewHTTP(t, mcptest.Upstream{Methods: map[string]mcptest.Method{
		"tools/call": func(mcptest.Request) mcptest.Reply { return mcptest.Reply{Result: map[string]any{"ok": true}} },
		"tools/list": func(mcptest.Request) mcptest.Reply { return mcptest.Reply{Status: 503, Body: []byte("busy")} },
	}})

	result := post(t, up.URL, call("tools/call"))
	status := post(t, up.URL, call("tools/list"))

	if !strings.Contains(result.Body, `"result":{"ok":true}`) {
		t.Errorf("tools/call = %s, want the overriding result", result.Body)
	}
	if status.Status != 503 || status.Body != "busy" {
		t.Errorf("tools/list = %d %q, want the bare 503 reply", status.Status, status.Body)
	}
}

func TestNewHTTP_interceptAnswersBeforeTheBodyIsRead(t *testing.T) {
	up := mcptest.NewHTTP(t, mcptest.Upstream{Intercept: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			return false
		}
		w.WriteHeader(http.StatusTeapot)
		return true
	}})

	got := post(t, up.URL+"/.well-known/oauth-authorization-server", "not json")

	if got.Status != http.StatusTeapot || len(up.Requests()) != 0 {
		t.Errorf("intercepted = %d with %d recorded, want 418 and nothing recorded", got.Status, len(up.Requests()))
	}
}
