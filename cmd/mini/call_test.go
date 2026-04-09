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
		{"mini flag", callFlags{mini: true}, "", callOutputMini},
		{"json flag", callFlags{json: true}, "", callOutputJSON},
		{"cfg mini", callFlags{}, "mini", callOutputMini},
		{"default", callFlags{}, "", callOutputJSON},
		{"raw wins over mini", callFlags{raw: true, mini: true}, "", callOutputRaw},
		{"cfg overridden by json flag", callFlags{json: true}, "mini", callOutputJSON},
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
