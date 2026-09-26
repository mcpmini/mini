//go:build test

package provider_test

import "github.com/mcpmini/mini/internal/auth"

func init() {
	auth.UseLoopbackHTTPClient()
}
