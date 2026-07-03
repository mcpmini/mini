//go:build test

package forge_test

import (
	"context"
	"encoding/json"
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

func TestExecute_largeStderrReturnsPromptlyWithCappedCapture(t *testing.T) {
	requireDeno(t)
	code := `async () => { console.error("e".repeat(256 * 1024)); return "done"; }`
	start := time.Now()
	got, err := forge.Execute(context.Background(), forge.Params{
		Code:    code,
		Timeout: 20 * time.Second,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if string(got) != `"done"` {
		t.Errorf("result = %s, want %q", got, "done")
	}
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %v, want prompt return (stderr past the cap must be drained, not block the child)", elapsed)
	}

	got, err = forge.Execute(context.Background(), forge.Params{Code: "async () => 2"})
	if err != nil {
		t.Fatalf("follow-up Execute after large-stderr run: %v", err)
	}
	if string(got) != "2" {
		t.Errorf("follow-up result = %s, want 2", got)
	}
}

func TestExecute_largeReturnValueIsOutputTooLargeNotRunnerError(t *testing.T) {
	requireDeno(t)
	code := `async () => "x".repeat(20_000_000)`
	_, err := forge.Execute(context.Background(), forge.Params{
		Code:    code,
		Timeout: 20 * time.Second,
	})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindOutputTooLarge {
		t.Errorf("Kind = %q, want %q (a partial writeSync must not surface as \"no result emitted\")", fe.Kind, forge.KindOutputTooLarge)
	}
}

func TestExecute_moderatelyLargeResultRoundTripsIntact(t *testing.T) {
	requireDeno(t)
	code := `async () => "y".repeat(1_000_000)`
	got, err := forge.Execute(context.Background(), forge.Params{
		Code:    code,
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var s string
	if err := json.Unmarshal(got, &s); err != nil {
		t.Fatalf("result is not a JSON string: %v (%s)", err, got)
	}
	if len(s) != 1_000_000 {
		t.Fatalf("len(result) = %d, want 1000000", len(s))
	}
	if s[0] != 'y' || s[len(s)-1] != 'y' {
		t.Errorf("result = %q...%q, want all 'y'", s[:1], s[len(s)-1:])
	}
}
