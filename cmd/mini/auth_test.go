package main

import (
	"testing"
)

func TestResolveOpenerCmd(t *testing.T) {
	tests := []struct {
		name      string
		perServer string
		global    string
		want      string
	}{
		{"per-server wins over global", "per-server-cmd", "global-cmd", "per-server-cmd"},
		{"global used when no per-server", "", "global-cmd", "global-cmd"},
		{"neither set returns empty", "", "", ""},
		{"per-server with args wins", "open -a Firefox", "global-cmd", "open -a Firefox"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOpenerCmd(tc.perServer, tc.global)
			if got != tc.want {
				t.Errorf("resolveOpenerCmd(%q, %q) = %q, want %q", tc.perServer, tc.global, got, tc.want)
			}
		})
	}
}

func TestAuthOpener_usesPlatformDefaultWhenNeitherSet(t *testing.T) {
	var called bool
	orig := openBrowser
	openBrowser = func(url string) error { called = true; return nil }
	t.Cleanup(func() { openBrowser = orig })

	opener := authOpener("", "", false)
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

	opener := authOpener("echo", "", false)
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

	opener := authOpener("echo", "global-cmd", true)
	if err := opener("http://example.com"); err != nil {
		t.Errorf("disabled opener returned error: %v", err)
	}
	if called {
		t.Error("platform opener should not be called when disabled")
	}
}
