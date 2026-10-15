//go:build test

package main

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/testutil"
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

func TestMissingDefaultConfigRequiresExplicitDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	run := func(args ...string) error {
		t.Helper()
		cmd := newRootCmd()
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))
		cmd.SetArgs(args)
		return cmd.Execute()
	}
	err := run("ls")
	if err == nil || !strings.Contains(err.Error(), "cannot determine default config directory; pass --config DIR") {
		t.Fatalf("ls without --config error = %v, want explicit config guidance", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".mini")); !os.IsNotExist(statErr) {
		t.Fatalf("working-directory .mini stat error = %v, want no directory created", statErr)
	}
	if commandErr := run("--config", filepath.Join(dir, "selected"), "ls"); commandErr != nil {
		t.Fatalf("ls with explicit --config: %v", commandErr)
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"version"}, {"--version"}} {
		if commandErr := run(args...); commandErr != nil {
			t.Errorf("%v: %v", args, commandErr)
		}
	}
	err = run("--config", dir, "call", "server", "tool", "--json", "--raw")
	if err == nil || !strings.Contains(err.Error(), "choose only one output mode") {
		t.Errorf("call PreRunE error = %v, want output mode validation", err)
	}
}

func TestCommandLogsGoToAFileNotTheTerminal(t *testing.T) {
	var terminal bytes.Buffer
	previous := slog.Default()
	terminalLogger := slog.New(slog.NewTextHandler(&terminal, nil))
	slog.SetDefault(terminalLogger)
	t.Cleanup(func() { slog.SetDefault(previous) })
	configDir := t.TempDir()
	failed := errors.New("server unhealthy")
	cmd := &cobra.Command{Use: "status", RunE: func(*cobra.Command, []string) error {
		slog.Warn("upstream http request retrying")
		return failed
	}}
	logToFileWhileRunning(cmd, &rootOptions{configDir: configDir})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(nil)

	if err := cmd.Execute(); !errors.Is(err, failed) {
		t.Fatalf("err = %v, want the command's own error", err)
	}

	if terminal.Len() != 0 {
		t.Errorf("terminal got %q, want no log lines", terminal.String())
	}
	logged := testutil.ReadFile(t, filepath.Join(configDir, "internal", "cli.log"))
	if !strings.Contains(string(logged), "upstream http request retrying") {
		t.Errorf("cli.log = %q, want the warning", logged)
	}
	if slog.Default() != terminalLogger {
		t.Error("logging was not handed back after the failed command")
	}
}
