//go:build integration

package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

func TestIntegrationStdioEnvironment_overlaysParent(t *testing.T) {
	t.Setenv("PATH", "/path/to/parent/bin")
	t.Setenv("MINI_ENV_BASE", "parent")
	t.Setenv("MINI_ENV_ADDED", "")
	for _, tc := range []struct {
		name  string
		env   []string
		base  string
		added string
	}{
		{name: "inheritsWithoutEntries", base: "parent"},
		{name: "addsWithoutLosingPath", env: []string{"MINI_ENV_ADDED=synthetic"}, base: "parent", added: "synthetic"},
		{name: "overridesParent", env: []string{"MINI_ENV_BASE=server"}, base: "server"},
		{name: "emptyOverride", env: []string{"MINI_ENV_BASE="}},
		{name: "lastOverrideWins", env: []string{"MINI_ENV_BASE=first", "MINI_ENV_BASE=last"}, base: "last"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := subprocessEnvironment(t, tc.env)
			want := map[string]string{
				"PATH":           "/path/to/parent/bin",
				"MINI_ENV_BASE":  tc.base,
				"MINI_ENV_ADDED": tc.added,
			}
			if !maps.Equal(got, want) {
				t.Fatalf("child environment = %v, want %v", got, want)
			}
		})
	}
}

func subprocessEnvironment(t *testing.T, env []string) map[string]string {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := startSubprocess(StdioCommand{
		Command: bin, Args: []string{"-test.run=^TestIntegrationStdioEnvironment_helper$", "--", "mini-env-helper"},
		Env: env, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := conn.Call(ctx, "environment", nil)
	if err != nil {
		t.Fatal(err)
	}
	return decodeEnvironment(t, raw)
}

func decodeEnvironment(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var env map[string]string
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestIntegrationStdioEnvironment_helper(t *testing.T) {
	if !slices.Contains(os.Args, "mini-env-helper") {
		return
	}
	scanner := NewScanner(os.Stdin)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		sendResponse(os.Stdout, req.ID, map[string]string{
			"PATH": os.Getenv(
				"PATH",
			),
			"MINI_ENV_BASE":  os.Getenv("MINI_ENV_BASE"),
			"MINI_ENV_ADDED": os.Getenv("MINI_ENV_ADDED"),
		})
	}
	os.Exit(0)
}

func TestIntegrationStdioConnection_aFailedHandshakeReturnsWhenAChildOfTheServerHoldsStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	// The server never answers, so the handshake times out and the connection is closed. Its
	// background sleep keeps stderr open after the server itself is killed.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := NewStdioConnection(ctx, StdioCommand{
			Command: "sh",
			Args:    []string{"-c", "sleep 60 & sleep 60"},
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		returned <- err
	}()
	select {
	case err := <-returned:
		if err == nil {
			t.Error("a server that never answers completed the handshake")
		}
	case <-time.After(time.Second + stdioWaitDelay + 5*time.Second):
		t.Fatal("still waiting for the server's child, which holds its stderr")
	}
}
