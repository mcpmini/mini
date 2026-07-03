//go:build test

package forge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/forge"
)

func BenchmarkExecuteTrivial(b *testing.B) {
	if _, err := forge.Execute(context.Background(), forge.Params{Code: "async (i) => i"}); err != nil {
		b.Skipf("deno unavailable or execution failed: %v", err)
	}
	for b.Loop() {
		_, err := forge.Execute(context.Background(), forge.Params{
			Code:  "async (i) => i",
			Input: json.RawMessage("1"),
		})
		if err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}
