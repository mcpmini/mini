//go:build test

package forge_test

import (
	"context"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/forge"
)

func TestExecute_timeout(t *testing.T) {
	requireDeno(t)
	start := time.Now()
	_, err := forge.Execute(context.Background(), forge.Params{
		Code:    "async () => { while (true) {} }",
		Timeout: time.Second,
	})
	elapsed := time.Since(start)

	fe := asForgeError(t, err)
	if fe.Kind != forge.KindTimeout {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindTimeout)
	}
	if elapsed > 4*time.Second {
		t.Errorf("elapsed = %v, want well under 4s (process must be killed on timeout)", elapsed)
	}
}

func TestExecute_cancellation(t *testing.T) {
	requireDeno(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := forge.Execute(ctx, forge.Params{
		Code:    "async () => { while (true) {} }",
		Timeout: 30 * time.Second,
	})
	elapsed := time.Since(start)

	fe := asForgeError(t, err)
	if fe.Kind != forge.KindCancelled {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindCancelled)
	}
	if elapsed > 4*time.Second {
		t.Errorf("elapsed = %v, want promptly after cancellation (well under 4s)", elapsed)
	}
}

func TestExecute_outputTooLarge(t *testing.T) {
	requireDeno(t)
	code := `async () => {
		const chunk = "x".repeat(65536);
		while (true) { console.log(chunk); }
	}`
	start := time.Now()
	_, err := forge.Execute(context.Background(), forge.Params{
		Code:    code,
		Timeout: 20 * time.Second,
	})
	elapsed := time.Since(start)

	fe := asForgeError(t, err)
	if fe.Kind != forge.KindOutputTooLarge {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindOutputTooLarge)
	}
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %v, want the 8MB cap to be hit well before the 20s timeout", elapsed)
	}
}
