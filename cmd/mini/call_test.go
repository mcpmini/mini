//go:build test

package main

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestResolveCallOutput(t *testing.T) {
	cases := []struct {
		name      string
		f         callFlags
		cfgFormat string
		want      callOutput
	}{
		{"raw flag", callFlags{raw: true}, "", callOutputRaw},
		{"toon flag", callFlags{toon: true}, "", callOutputToon},
		{"json flag", callFlags{json: true}, "", callOutputJSON},
		{"cfg toon", callFlags{}, "toon", callOutputToon},
		{"default", callFlags{}, "", callOutputJSON},
		{"raw wins over toon", callFlags{raw: true, toon: true}, "", callOutputRaw},
		{"cfg overridden by json flag", callFlags{json: true}, "toon", callOutputJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveCallOutput(tc.f, tc.cfgFormat)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
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
