//go:build test

package forge_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mcpmini/mini/internal/forge"
)

func TestExecute_netGrantAllowsListedHost(t *testing.T) {
	requireDeno(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("hello from grant")) //nolint:errcheck // test server, best-effort write
	}))
	defer ts.Close()

	code := `async (input) => { const r = await fetch(input.url); return await r.text(); }`
	got, err := forge.Execute(context.Background(), forge.Params{
		Code:  code,
		Input: json.RawMessage(`{"url":"` + ts.URL + `"}`),
		Net:   []string{listenerHostPort(t, ts)},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, `"hello from grant"`)
}

func TestExecute_netGrantDeniesUnlistedHost(t *testing.T) {
	requireDeno(t)
	granted := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer granted.Close()
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer other.Close()

	code := `async (input) => { await fetch(input.url); }`
	_, err := forge.Execute(context.Background(), forge.Params{
		Code:  code,
		Input: json.RawMessage(`{"url":"` + other.URL + `"}`),
		Net:   []string{listenerHostPort(t, granted)},
	})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindRuntime {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindRuntime)
	}
	if !containsAny(fe.Message, "net access") {
		t.Errorf("Message = %q, want it to mention net access", fe.Message)
	}
	if !containsAny(fe.Message, "code_mode.url_allow_list") {
		t.Errorf("Message = %q, want the url_allow_list hint", fe.Message)
	}
}

func TestExecute_dangerousAllowAllNetFetchesWithoutNetEntries(t *testing.T) {
	requireDeno(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("wide open")) //nolint:errcheck // test server, best-effort write
	}))
	defer ts.Close()

	code := `async (input) => { const r = await fetch(input.url); return await r.text(); }`
	got, err := forge.Execute(context.Background(), forge.Params{
		Code:                 code,
		Input:                json.RawMessage(`{"url":"` + ts.URL + `"}`),
		DangerousAllowAllNet: true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, `"wide open"`)
}

func TestExecute_envGrantAllowsListedVar(t *testing.T) {
	requireDeno(t)
	t.Setenv("FORGE_TEST_TOKEN", "tok-123")
	got, err := forge.Execute(context.Background(), forge.Params{
		Code: `async () => Deno.env.get("FORGE_TEST_TOKEN")`,
		Env:  []string{"FORGE_TEST_TOKEN"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, `"tok-123"`)
}

func TestExecute_envGrantDeniesUnlistedVar(t *testing.T) {
	requireDeno(t)
	t.Setenv("FORGE_TEST_TOKEN", "tok-123")
	_, err := forge.Execute(context.Background(), forge.Params{
		Code: `async () => Deno.env.get("FORGE_TEST_TOKEN")`,
	})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindRuntime {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindRuntime)
	}
}

func TestExecute_envGrantedButUnsetYieldsUndefinedWithoutFailing(t *testing.T) {
	requireDeno(t)
	got, err := forge.Execute(context.Background(), forge.Params{
		Code: `async () => Deno.env.get("FORGE_TEST_UNSET_VAR") ?? null`,
		Env:  []string{"FORGE_TEST_UNSET_VAR"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, "null")
}

func listenerHostPort(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host:port from %q: %v", u.Host, err)
	}
	return "127.0.0.1:" + port
}
