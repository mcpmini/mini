//go:build test

package main

import "github.com/mcpmini/mini/internal/auth"

func init() {
	auth.UseLoopbackHTTPClient()
	auth.UseEphemeralCallbackPort()
	daemonReacher = func(string) daemonReach { return unreachableDaemon }
}
