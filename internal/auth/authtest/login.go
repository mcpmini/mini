package authtest

import (
	"net"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

// StartLogin starts a BrowserLogin on a free IPv4 loopback port and closes it at test cleanup.
func StartLogin(t *testing.T, ac *config.AuthConfig) *auth.BrowserLogin {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	login, err := auth.StartBrowserLogin(ac, listener)
	if err != nil {
		t.Fatalf("StartBrowserLogin: %v", err)
	}
	t.Cleanup(func() { login.Close() }) //nolint:errcheck
	return login
}

func RequireCallbackPortReleased(t *testing.T, login *auth.BrowserLogin) {
	t.Helper()
	listener, err := net.Listen("tcp", LoopbackRedirectURI(t, login.AuthURL()).Host)
	if err != nil {
		t.Fatalf("callback port still bound: %v", err)
	}
	listener.Close()
}
