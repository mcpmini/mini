package main

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func oauthServerWithBrowserCmd(cmd string) config.ServerConfig {
	return config.ServerConfig{Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, BrowserCmd: cmd}}
}

func TestAuthOpener_usesPlatformDefaultWhenNeitherSet(t *testing.T) {
	var called bool
	orig := openBrowser
	openBrowser = func(url string) error { called = true; return nil }
	t.Cleanup(func() { openBrowser = orig })

	opener := authOpener(&config.Config{}, config.ServerConfig{})
	_ = opener("http://example.com")
	if !called {
		t.Error("expected platform opener to be called when neither per-server nor global cmd is set")
	}
}

func TestAuthOpener_skipsPlatformDefaultWhenCmdSet(t *testing.T) {
	var called bool
	orig := openBrowser
	openBrowser = func(url string) error { called = true; return nil }
	t.Cleanup(func() { openBrowser = orig })

	opener := authOpener(&config.Config{}, oauthServerWithBrowserCmd("echo"))
	_ = opener("http://example.com")
	if called {
		t.Error("platform opener should not be called when per-server cmd is set")
	}
}

func TestAuthOpener_disabledSkipsAll(t *testing.T) {
	var called bool
	orig := openBrowser
	openBrowser = func(url string) error { called = true; return nil }
	t.Cleanup(func() { openBrowser = orig })

	opener := authOpener(&config.Config{BrowserCommand: "global-cmd", DisableAuthBrowserOpen: true}, oauthServerWithBrowserCmd("echo"))
	if err := opener("http://example.com"); err != nil {
		t.Errorf("disabled opener returned error: %v", err)
	}
	if called {
		t.Error("platform opener should not be called when disabled")
	}
}
