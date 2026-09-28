package authtest

import (
	"net"
	"net/http"
	"net/url"
	"testing"
)

// LoopbackRedirectURI returns authURL's redirect_uri with its host pinned to 127.0.0.1. Test
// callback listeners bind IPv4 loopback on an ephemeral port, and dialing "localhost" can reach
// an unrelated [::1] listener that happens to hold the same port number.
func LoopbackRedirectURI(t *testing.T, authURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	redirect, err := url.Parse(parsed.Query().Get("redirect_uri"))
	if err != nil || redirect.Port() == "" {
		t.Fatalf("auth URL has no usable redirect_uri: %s", authURL)
	}
	redirect.Host = net.JoinHostPort("127.0.0.1", redirect.Port())
	return redirect
}

// CompleteAuthorization makes the callback request a browser makes after the user approves
// authURL: the flow's state and the given code. The response is not checked: once the code is
// delivered the flow may close its callback server before the response is fully written.
func CompleteAuthorization(t *testing.T, authURL, code string) {
	t.Helper()
	callback := LoopbackRedirectURI(t, authURL)
	parsed, _ := url.Parse(authURL)
	callback.RawQuery = url.Values{"code": {code}, "state": {parsed.Query().Get("state")}}.Encode()
	if resp, err := http.Get(callback.String()); err == nil {
		resp.Body.Close()
	}
}
