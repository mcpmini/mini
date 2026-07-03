//go:build test

package forge

import (
	"context"
	"encoding/json"
)

// ExecuteWithEnv is a test-only seam: it lets internal/forge tests point
// DENO_DIR at an isolated cache directory instead of exposing that knob on
// the public Params type.
func ExecuteWithEnv(ctx context.Context, p Params, extraEnv []string) (json.RawMessage, error) {
	return execute(ctx, p, extraEnv)
}
