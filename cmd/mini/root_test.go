//go:build test

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentConfigPosition(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		args []string
	}{
		{"before subcommand", []string{"--config", dir, "ls"}},
		{"after subcommand", []string{"ls", "--config", dir}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "no servers configured") {
				t.Fatalf("output %q did not use %s", out.String(), filepath.Clean(dir))
			}
		})
	}
}
