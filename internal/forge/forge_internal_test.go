//go:build test

package forge

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunArgs_flagSelection(t *testing.T) {
	t.Run("no packages keeps stage-1 flags exactly", func(t *testing.T) {
		want := "run --no-prompt --no-remote -"
		if got := strings.Join(runArgs(nil), " "); got != want {
			t.Errorf("runArgs(nil) = %q, want %q", got, want)
		}
	})
	t.Run("packages switch to cached-only", func(t *testing.T) {
		want := "run --no-prompt --cached-only -"
		if got := strings.Join(runArgs([]string{"npm:zod@3"}), " "); got != want {
			t.Errorf("runArgs(packages) = %q, want %q", got, want)
		}
	})
}

func TestBuildProgram_embedding(t *testing.T) {
	program := buildProgram("async (i) => i", []byte(`{"n":1}`), "MARKERX")

	t.Run("marker embedded", func(t *testing.T) {
		if !strings.Contains(program, "MARKERX") {
			t.Error("program does not contain the marker")
		}
	})
	t.Run("code wrapped as base64 module", func(t *testing.T) {
		wrapped := base64.StdEncoding.EncodeToString([]byte("export default (async (i) => i\n);"))
		if !strings.Contains(program, wrapped) {
			t.Error("program does not contain the base64-wrapped user module")
		}
	})
	t.Run("input embedded as JS literal", func(t *testing.T) {
		if !strings.Contains(program, `const __input = {"n":1};`) {
			t.Error("program does not embed the input JSON")
		}
	})
	t.Run("empty input becomes null", func(t *testing.T) {
		if !strings.Contains(buildProgram("async () => 1", nil, "M"), "const __input = null;") {
			t.Error("program does not default missing input to null")
		}
	})
}

func TestClassify_errorPaths(t *testing.T) {
	bg := context.Background()
	marker := "MK"

	kindOf := func(t *testing.T, result runResult, parent, run context.Context) ErrorKind {
		t.Helper()
		_, err := classify(result, parent, run, marker)
		var fe *Error
		if !errors.As(err, &fe) {
			t.Fatalf("error = %v, want *forge.Error", err)
		}
		return fe.Kind
	}

	t.Run("output too large", func(t *testing.T) {
		if k := kindOf(t, runResult{outputTooLarge: true}, bg, bg); k != KindOutputTooLarge {
			t.Errorf("Kind = %q, want %q", k, KindOutputTooLarge)
		}
	})
	t.Run("no marker with cancelled parent", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(bg)
		cancel()
		if k := kindOf(t, runResult{}, cancelled, cancelled); k != KindCancelled {
			t.Errorf("Kind = %q, want %q", k, KindCancelled)
		}
	})
	t.Run("no marker with expired run context", func(t *testing.T) {
		expired, cancel := context.WithDeadline(bg, time.Now().Add(-time.Second))
		defer cancel()
		if k := kindOf(t, runResult{}, bg, expired); k != KindTimeout {
			t.Errorf("Kind = %q, want %q", k, KindTimeout)
		}
	})
	t.Run("no marker with wait error surfaces stderr", func(t *testing.T) {
		result := runResult{stderr: []byte("deno exploded"), waitErr: errors.New("exit status 1")}
		_, err := classify(result, bg, bg, marker)
		var fe *Error
		errors.As(err, &fe)
		if fe.Kind != KindRunner || !strings.Contains(fe.Message, "deno exploded") {
			t.Errorf("got kind %q message %q, want runner error with stderr", fe.Kind, fe.Message)
		}
	})
	t.Run("no marker with clean exit", func(t *testing.T) {
		if k := kindOf(t, runResult{}, bg, bg); k != KindRunner {
			t.Errorf("Kind = %q, want %q", k, KindRunner)
		}
	})
	t.Run("malformed payload", func(t *testing.T) {
		if k := kindOf(t, runResult{stdout: []byte("\nMK{not json")}, bg, bg); k != KindRunner {
			t.Errorf("Kind = %q, want %q", k, KindRunner)
		}
	})
	t.Run("error payload maps kind verbatim", func(t *testing.T) {
		result := runResult{stdout: []byte("\nMK{\"error\":{\"kind\":\"syntax\",\"message\":\"bad\"}}")}
		if k := kindOf(t, result, bg, bg); k != KindSyntax {
			t.Errorf("Kind = %q, want %q", k, KindSyntax)
		}
	})
}

func TestClassify_successPaths(t *testing.T) {
	bg := context.Background()

	t.Run("ok payload returned verbatim", func(t *testing.T) {
		got, err := classify(runResult{stdout: []byte("noise\nMK{\"ok\":[1,2]}")}, bg, bg, "MK")
		if err != nil || string(got) != "[1,2]" {
			t.Errorf("got %s, %v; want [1,2], nil", got, err)
		}
	})
	t.Run("last marker wins over printed fakes", func(t *testing.T) {
		stdout := []byte("\nMK{\"ok\":\"spoofed\"}\nMK{\"ok\":\"genuine\"}")
		got, err := classify(runResult{stdout: stdout}, bg, bg, "MK")
		if err != nil || string(got) != `"genuine"` {
			t.Errorf("got %s, %v; want \"genuine\", nil", got, err)
		}
	})
	t.Run("console output lands in error console tail", func(t *testing.T) {
		result := runResult{stdout: []byte("diagnostic\nMK{\"error\":{\"kind\":\"runtime\",\"message\":\"x\"}}")}
		_, err := classify(result, bg, bg, "MK")
		var fe *Error
		errors.As(err, &fe)
		if !strings.Contains(fe.Console, "diagnostic") {
			t.Errorf("Console = %q, want the pre-marker output", fe.Console)
		}
	})
}
