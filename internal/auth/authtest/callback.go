package authtest

import (
	"net"
	"net/http"
	"net/url"
	"testing"
)

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
	// "localhost" can resolve to an unrelated [::1] listener holding the same ephemeral port.
	redirect.Host = net.JoinHostPort("127.0.0.1", redirect.Port())
	return redirect
}

func CompleteAuthorization(t *testing.T, authURL, code string) {
	t.Helper()
	DeliverCallback(CallbackURL(t, authURL, code))
}

// CallbackURL is the redirect the browser makes once the user approves authURL's request.
func CallbackURL(t *testing.T, authURL, code string) string {
	t.Helper()
	callback := LoopbackRedirectURI(t, authURL)
	parsed, _ := url.Parse(authURL)
	callback.RawQuery = url.Values{"code": {code}, "state": {parsed.Query().Get("state")}}.Encode()
	return callback.String()
}

// DeliverCallback makes the browser's redirect. It takes no *testing.T, so it can run on its own
// goroutine while a test holds the login.
func DeliverCallback(callbackURL string) {
	// unchecked: once it has the code, the flow may close its server mid-response
	if resp, err := http.Get(callbackURL); err == nil {
		resp.Body.Close()
	}
}
