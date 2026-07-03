//go:build test

package main

import (
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestExtractConfigDir(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantDir  string
		wantRest []string
	}{
		{"no --config uses default", []string{"ls"}, "", []string{"ls"}},
		{"leading --config", []string{"--config", "/x", "ls"}, "/x", []string{"ls"}},
		{"--config= form", []string{"--config=/x", "ls"}, "/x", []string{"ls"}},
		{"--config after subcommand", []string{"rm", "svc", "--config", "/x"}, "/x", []string{"rm", "svc"}},
		{"last --config wins", []string{"--config", "/a", "ls", "--config", "/b"}, "/b", []string{"ls"}},
		{"stops at -- and leaves the rest untouched", []string{"call", "svc", "--", "--config", "/x"}, "", []string{"call", "svc", "--", "--config", "/x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, rest := extractConfigDir(tc.args)
			wantDir := tc.wantDir
			if wantDir == "" {
				wantDir = config.DefaultConfigDir()
			}
			if dir != wantDir {
				t.Errorf("dir: got %q, want %q", dir, wantDir)
			}
			if !slices.Equal(rest, tc.wantRest) {
				t.Errorf("rest: got %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

func TestHelpRequested(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"no args", nil, false},
		{"plain positionals", []string{"svc", "tool"}, false},
		{"-h present", []string{"svc", "-h"}, true},
		{"--help present", []string{"--help"}, true},
		{"-h after -- is literal, not a help request", []string{"svc", "--", "-h"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := helpRequested(tc.args); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
