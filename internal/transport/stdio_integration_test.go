//go:build integration

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
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
	conn, err := startSubprocess(context.Background(), StdioCommand{
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
	t.Parallel()
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
	t.Parallel()
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
			Args:    []string{"-c", "sleep 10 & sleep 60"},
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

func TestIntegrationStdioModernProcessNegotiatesListsAndCalls(t *testing.T) {
	conn := connectProtocolHelper(t, helperModern)
	if got := conn.negotiatedModernVersion(); got != ModernVersions[0] {
		t.Fatalf("negotiated version = %q, want modern %q", got, ModernVersions[0])
	}
	tools, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools) != 1 || string(tools[0].OutputSchema) != `{"type":"object","properties":{"ok":{"type":"boolean"}}}` {
		t.Fatalf("modern tools = %+v", tools)
	}
	raw, err := conn.Call(context.Background(), "tools/call", json.RawMessage(`{"name":"modern_tool","arguments":{}}`))
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if string(raw) != `{"content":[{"type":"text","text":"modern"}],"structuredContent":{"ok":true}}` {
		t.Fatalf("modern result = %s", raw)
	}
}

func TestIntegrationStdioProbeExitGetsOneLegacyReplacement(t *testing.T) {
	for _, mode := range []string{helperExitOnProbeOnce, helperReplyThenExitOnProbeOnce} {
		t.Run(mode, func(t *testing.T) {
			command, starts := countedProtocolHelper(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := NewStdioConnection(ctx, command)
			if err != nil {
				t.Fatalf("connect after probe exit: %v", err)
			}
			defer func() { _ = conn.Close() }()
			if _, callErr := conn.Call(ctx, "ping", nil); callErr != nil {
				t.Fatalf("ping replacement: %v", callErr)
			}
			if got := helperStarts(t, starts); got != 2 {
				t.Fatalf("helper process starts = %d, want one initial process and one replacement", got)
			}
		})
	}
}

func TestIntegrationStdioUnansweredProbeReplacesPoisonedProcessOnce(t *testing.T) {
	command, starts := countedProtocolHelper(t, helperUnresponsiveOnce)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := NewStdioConnection(ctx, command)
	if err != nil {
		t.Fatalf("connect through replacement: %v", err)
	}
	defer func() { _ = conn.Close() }()
	tools, err := conn.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "modern_tool" {
		t.Fatalf("tools/list on replacement = %+v, %v", tools, err)
	}
	if _, err := conn.Call(ctx, "tools/call", json.RawMessage(`{"name":"modern_tool","arguments":{}}`)); err != nil {
		t.Fatalf("tools/call on replacement: %v", err)
	}
	if got := helperStarts(t, starts); got != 2 {
		t.Fatalf("helper process starts = %d, want one initial process and one replacement", got)
	}
}

func TestIntegrationStdioUnansweredProbeAndReplacementFailureAreBoundedByCallerDeadline(t *testing.T) {
	command, starts := countedProtocolHelper(t, helperUnresponsiveAlways)
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	_, err := NewStdioConnection(ctx, command)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "did not answer the modern probe") ||
		!strings.Contains(err.Error(), "restarting it without the modern probe failed too") {
		t.Fatalf("connect error = %v, want bounded unanswered-probe and replacement failure", err)
	}
	if got := helperStarts(t, starts); got != 2 {
		t.Fatalf("helper process starts = %d, want one initial process and one failed replacement", got)
	}
}

func TestIntegrationStdioCallerCancellationDuringUnansweredProbeDoesNotReplace(t *testing.T) {
	command, starts := countedProtocolHelper(t, helperUnresponsiveOnce)
	marker := t.TempDir() + "/discovered"
	command.Env = append(command.Env, "MINI_STDIO_DISCOVERED="+marker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := NewStdioConnection(ctx, command)
		result <- err
	}()
	waitForHelperMarker(t, marker)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("connect error = %v, want context.Canceled", err)
	}
	if got := helperStarts(t, starts); got != 1 {
		t.Fatalf("helper process starts = %d, want no replacement after cancellation", got)
	}
}

func TestIntegrationStdioCallReturnsAtItsDeadlineWhenTheServerStopsReadingStdin(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	conn, err := startSubprocess(context.Background(), StdioCommand{
		Command: "sh", Args: []string{"-c", "sleep 60"}, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	largerThanThePipeBuffer := json.RawMessage(`"` + strings.Repeat("x", 1<<20) + `"`)
	returned := make(chan error, 1)
	go func() { _, err := conn.Call(ctx, "ping", largerThanThePipeBuffer); returned <- err }()
	select {
	case err := <-returned:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("Call error = %v, want the interrupted write", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call stayed blocked writing to a server that stopped reading stdin")
	}
	if conn.Health(context.Background()) == nil {
		t.Fatal("a half-written request left the connection usable")
	}
}

func TestIntegrationStdioStartupCrashFailsOnceWithTheOriginalExit(t *testing.T) {
	command, starts := countedProtocolHelper(t, helperCrash)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := NewStdioConnection(ctx, command)
	if err == nil || !strings.Contains(err.Error(), "upstream exited during setup (exit status 1)") {
		t.Fatalf("connect to a crashing server: %v, want the original exit named", err)
	}
	if got := helperStarts(t, starts); got != 2 {
		t.Fatalf("helper process starts = %d, want one replacement and no more", got)
	}
}

func countedProtocolHelper(t *testing.T, mode string) (command StdioCommand, starts string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	starts = t.TempDir() + "/starts"
	command = protocolHelperCommand(bin, mode)
	command.Env = append(command.Env, "MINI_STDIO_STARTS="+starts)
	return command, starts
}

func connectProtocolHelper(t *testing.T, mode string) *StdioConnection {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := NewStdioConnection(context.Background(), protocolHelperCommand(bin, mode))
	if err != nil {
		t.Fatalf("connect %s helper: %v", mode, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func helperStarts(t *testing.T, path string) int {
	t.Helper()
	starts, err := countHelperStarts(path)
	if err != nil {
		t.Fatal(err)
	}
	return starts
}

func protocolHelperCommand(bin, mode string) StdioCommand {
	return StdioCommand{
		Command: bin,
		Args:    []string{"-test.run=^TestIntegrationStdioProtocolHelper$", "--", "stdio-protocol-helper", mode},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

const (
	helperModern                   = "modern"
	helperExitOnProbeOnce          = "exit-on-probe-once"
	helperReplyThenExitOnProbeOnce = "reply-then-exit-on-probe-once"
	helperUnresponsiveOnce         = "unresponsive-once"
	helperUnresponsiveAlways       = "unresponsive-always"
	helperCrash                    = "crash"
)

func TestIntegrationStdioProtocolHelper(t *testing.T) {
	mode := integrationHelperMode("stdio-protocol-helper")
	if mode == "" {
		return
	}
	starts := os.Getenv("MINI_STDIO_STARTS")
	if starts != "" {
		appendHelperStart(starts)
	}
	if mode == helperCrash {
		os.Exit(1)
	}
	firstStart := false
	if starts != "" {
		count, err := countHelperStarts(starts)
		firstStart = err == nil && count == 1
	}
	initialized := false
	scanner := NewScanner(os.Stdin)
	for scanner.Scan() {
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		if request.Method == "server/discover" {
			markHelperFile(os.Getenv("MINI_STDIO_DISCOVERED"))
		}
		if shouldIgnoreRequest(mode, firstStart, request) {
			time.Sleep(time.Hour)
		}
		if mode == helperUnresponsiveOnce && !firstStart && !initialized && request.Method != "initialize" {
			os.Exit(3)
		}
		if request.Method == "initialize" {
			initialized = true
		}
		answerProtocolRequest(mode, firstStart, request)
	}
	os.Exit(0)
}

func shouldIgnoreRequest(mode string, firstStart bool, request Request) bool {
	return (mode == helperUnresponsiveAlways || mode == helperUnresponsiveOnce && firstStart) &&
		(request.Method == "server/discover" || request.Method == "initialize")
}

func answerProtocolRequest(mode string, firstStart bool, request Request) {
	switch request.Method {
	case "server/discover":
		answerProtocolProbe(mode, firstStart, request)
	case "initialize":
		sendResponse(os.Stdout, request.ID, InitializeResult{ProtocolVersion: LatestLegacyVersion})
	case "tools/list":
		sendResponse(os.Stdout, request.ID, ToolsListResult{Tools: []ToolDefinition{{
			Name:         "modern_tool",
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
		}}})
	case "tools/call":
		sendResponse(os.Stdout, request.ID,
			json.RawMessage(`{"content":[{"type":"text","text":"modern"}],"structuredContent":{"ok":true}}`))
	case "ping":
		sendResponse(os.Stdout, request.ID, json.RawMessage(`{}`))
	}
}

func answerProtocolProbe(mode string, firstStart bool, request Request) {
	if mode == helperUnresponsiveOnce || mode == helperUnresponsiveAlways {
		return
	}
	if mode == helperModern {
		sendResponse(os.Stdout, request.ID, DiscoverResult{SupportedVersions: ModernVersions})
		return
	}
	if firstStart && mode == helperExitOnProbeOnce {
		os.Exit(0)
	}
	writeRPCError(rpcErrorParams{
		Writer:  os.Stdout,
		ID:      request.ID,
		Code:    CodeMethodNotFound,
		Message: "legacy",
		Data:    nil,
	})
	if firstStart && mode == helperReplyThenExitOnProbeOnce {
		os.Exit(0)
	}
}

func waitForHelperMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("helper did not create marker %q", path)
		}
	}
}

func markHelperFile(path string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	if err := file.Close(); err != nil {
		os.Exit(2)
	}
}

func integrationHelperMode(marker string) string {
	for i, arg := range os.Args {
		if arg == marker && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func appendHelperStart(path string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	fmt.Fprintln(file, "start")
	file.Close()
}

func countHelperStarts(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	contents, err := io.ReadAll(file)
	return strings.Count(string(contents), "start\n"), err
}
