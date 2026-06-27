//go:build test

package auth_test

import "github.com/mcpmini/mini/internal/auth"

func init() {
	auth.UseLoopbackHTTPClient()
	auth.UseLoopbackURLValidation()
}
