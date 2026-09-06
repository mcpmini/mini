//go:build test

package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/response"
)

func TestResolveCallOutput(t *testing.T) {
	cases := []struct {
		name       string
		f          callFlags
		projFormat string
		cfgFormat  string
		want       callOutput
	}{
		{"raw flag", callFlags{raw: true}, "", "", callOutputRaw},
		{"toon flag", callFlags{toon: true}, "", "", callOutputToon},
		{"json flag", callFlags{json: true}, "", "", callOutputJSON},
		{"cfg toon", callFlags{}, "", "toon", callOutputToon},
		{"default", callFlags{}, "", "", callOutputJSON},
		{"raw wins over toon", callFlags{raw: true, toon: true}, "", "", callOutputRaw},
		{"cfg overridden by json flag", callFlags{json: true}, "", "toon", callOutputJSON},
		{"projection toon applies without -t flag", callFlags{}, "toon", "", callOutputToon},
		{"global toon overridden by exact-tool json projection", callFlags{}, "json", "toon", callOutputJSON},
		{"-j flag beats toon projection", callFlags{json: true}, "toon", "", callOutputJSON},
		{"-t flag beats json projection", callFlags{toon: true}, "json", "toon", callOutputToon},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveCallOutput(tc.f, tc.projFormat, tc.cfgFormat)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrintCallOutputToonReturnsEncodingError(t *testing.T) {
	nested := map[string]any{"leaf": "value"}
	for i := 0; i < 70; i++ {
		nested = map[string]any{"level": nested, "other": i}
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = originalStdout })

	gotErr := printCallOutput("gh", "list_issues", &response.Envelope{Data: nested}, callOutputToon)
	w.Close()
	output, readErr := io.ReadAll(r)
	r.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "nesting depth exceeds") {
		t.Fatalf("expected TOON encoding error, got: %v", gotErr)
	}
	if len(output) != 0 {
		t.Fatalf("failed TOON encoding printed output instead of returning an error: %q", output)
	}
}

func TestResolveCallProjection(t *testing.T) {
	exact := &config.ProjectionConfig{}
	wildcard := &config.ProjectionConfig{}

	t.Run("nil projections", func(t *testing.T) {
		if resolveCallProjection(&config.ServerConfig{}, "tool") != nil {
			t.Error("expected nil")
		}
	})
	t.Run("exact match", func(t *testing.T) {
		sc := &config.ServerConfig{Projections: map[string]*config.ProjectionConfig{"tool": exact}}
		if resolveCallProjection(sc, "tool") != exact {
			t.Error("expected exact match")
		}
	})
	t.Run("wildcard fallback", func(t *testing.T) {
		sc := &config.ServerConfig{Projections: map[string]*config.ProjectionConfig{"*": wildcard}}
		if resolveCallProjection(sc, "other") != wildcard {
			t.Error("expected wildcard")
		}
	})
	t.Run("exact wins over wildcard", func(t *testing.T) {
		sc := &config.ServerConfig{Projections: map[string]*config.ProjectionConfig{
			"tool": exact,
			"*":    wildcard,
		}}
		if resolveCallProjection(sc, "tool") != exact {
			t.Error("expected exact to win")
		}
	})
	t.Run("no match returns nil", func(t *testing.T) {
		sc := &config.ServerConfig{Projections: map[string]*config.ProjectionConfig{"other": exact}}
		if resolveCallProjection(sc, "tool") != nil {
			t.Error("expected nil")
		}
	})
}

func TestCallPermissionError(t *testing.T) {
	t.Run("call blocks protected", func(t *testing.T) {
		perm := &config.PermissionsConfig{Protected: []string{"DeleteRepo"}}
		code, _, blocked := callPermissionError(perm, "deleterepo", false)
		if !blocked || code != 2 {
			t.Fatalf("expected protected block, got blocked=%v code=%d", blocked, code)
		}
	})
	t.Run("perm call allows protected", func(t *testing.T) {
		perm := &config.PermissionsConfig{Protected: []string{"delete_repo"}}
		_, _, blocked := callPermissionError(perm, "delete_repo", true)
		if blocked {
			t.Fatal("perm-call should allow protected tools")
		}
	})
	t.Run("perm call blocks hidden", func(t *testing.T) {
		perm := &config.PermissionsConfig{Hidden: []string{"AdminTool"}}
		code, _, blocked := callPermissionError(perm, "admintool", true)
		if !blocked || code != 1 {
			t.Fatalf("expected hidden block, got blocked=%v code=%d", blocked, code)
		}
	})
	t.Run("hidden default wins", func(t *testing.T) {
		perm := &config.PermissionsConfig{Default: string(config.PermHidden)}
		code, _, blocked := callPermissionError(perm, "anything", true)
		if !blocked || code != 1 {
			t.Fatalf("expected hidden default block, got blocked=%v code=%d", blocked, code)
		}
	})
}
