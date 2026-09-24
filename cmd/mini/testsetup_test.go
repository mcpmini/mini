//go:build test

package main

import "github.com/mcpmini/mini/internal/auth"

func init() {
	// SSRFSafeDialer in auth.noRedirectClient blocks loopback; tests need it open.
	auth.UseLoopbackHTTPClient()
}
