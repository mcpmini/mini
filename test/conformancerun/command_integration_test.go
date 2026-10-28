//go:build integration && unix

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("MINI_RUNNER_HELPER"); mode != "" {
		runSuiteHelper(mode)
		return
	}
	os.Exit(m.Run())
}

func runSuiteHelper(mode string) {
	if mode == "results" || (mode == "mixed" && helperScenario() == "first") {
		writeHelperResults()
		return
	}
	if mode == "child" {
		runChildHelper()
		return
	}
	child, err := startChildHelper()
	if err != nil {
		panic(err)
	}
	fmt.Printf("child-pid=%d\npartial output retained\n", child.Process.Pid)
	if mode != "exit" {
		_ = child.Wait()
	}
}

func runChildHelper() {
	fmt.Fprintln(os.Stderr, "child stderr retained")
	ready := os.NewFile(3, "ready")
	if _, err := ready.Write([]byte{1}); err != nil {
		panic(err)
	}
	_ = ready.Close()
	time.Sleep(time.Minute)
}

func startChildHelper() (*exec.Cmd, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer read.Close()
	defer write.Close()
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), "MINI_RUNNER_HELPER=child")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	child.ExtraFiles = []*os.File{write}
	if err = child.Start(); err != nil {
		return nil, err
	}
	ready := make([]byte, 1)
	_, err = io.ReadFull(read, ready)
	return child, err
}

func helperScenario() string {
	for i, arg := range os.Args {
		if arg == "--scenario" && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func writeHelperResults() {
	dir := filepath.Join(os.Args[len(os.Args)-1], helperScenario())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		panic(err)
	}
	data := `[{"id":"fixture-exercised","status":"SUCCESS"}]`
	if helperScenario() == "broken" {
		data = "invalid JSON"
	}
	if err := os.WriteFile(
		filepath.Join(dir, "checks.json"),
		[]byte(data),
		0o600,
	); err != nil { //fileiolint:allow subprocess fixture writes suite artifact
		panic(err)
	}
	fmt.Println("Results saved to", dir)
}

func TestIntegrationRunnerDeadlineCleansChildrenAndPreservesOutput(t *testing.T) {
	for _, mode := range []string{"hang", "exit"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("MINI_RUNNER_HELPER", mode)
			t.Setenv("GORACE", "atexit_sleep_ms=0")
			env := helperRunEnv(t)
			output, _, err := runSuite(context.Background(), scenarioRun{Env: env, Name: "example"})
			if mode == "hang" && (err == nil || !strings.Contains(err.Error(), "runner scenario deadline")) {
				t.Fatalf("deadline error = %v", err)
			}
			if mode == "exit" && !errors.Is(err, exec.ErrWaitDelay) {
				t.Fatalf("inherited pipe error = %v", err)
			}
			if !strings.Contains(string(output), "partial output retained") ||
				!strings.Contains(string(output), "child stderr retained") {
				t.Fatalf("lost output: %s", output)
			}
			assertChildStopped(t, output)
		})
	}
}

func helperRunEnv(t *testing.T) runEnv {
	t.Helper()
	return runEnv{Suite: os.Args[0], Results: t.TempDir(), Limits: runLimits{
		Suite: 50 * time.Millisecond, Scenario: 300 * time.Millisecond, Wait: 50 * time.Millisecond, Total: time.Second,
	}}
}

func assertChildStopped(t *testing.T, output []byte) {
	t.Helper()
	_, rest, ok := strings.Cut(string(output), "child-pid=")
	if !ok {
		t.Fatalf("child PID missing: %s", output)
	}
	line := strings.SplitN(rest, "\n", 2)[0]
	pid, err := strconv.Atoi(line)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
		if err != nil || strings.Contains(string(status), "Z") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("suite child %d is still running", pid)
}

func TestIntegrationRunnerBudgetWritesPartialSummary(t *testing.T) {
	t.Setenv("MINI_RUNNER_HELPER", "mixed")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	env := helperRunEnv(t)
	env.Limits.Total = 250 * time.Millisecond
	outcomes, err := runSets(env, []scenarioSet{{Name: "budget", Scenarios: []string{"first", "second", "third"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 2 || !outcomes[0].passed() ||
		!strings.Contains(outcomes[1].Error, "total scenario budget exhausted") {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	summary, err := os.ReadFile(
		filepath.Join(env.Results, "summary.md"),
	) //fileiolint:allow inspect runner summary artifact
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summary), "1 of 2 scenarios passed") ||
		!strings.Contains(string(summary), "total scenario budget exhausted") {
		t.Fatalf("summary = %s", summary)
	}
	output, err := os.ReadFile(
		filepath.Join(env.Results, "budget", "second.log"),
	) //fileiolint:allow inspect preserved subprocess output
	if err != nil {
		t.Fatal(err)
	}
	assertChildStopped(t, output)
}

func TestIntegrationRunnerErrorsKeepEarlierAndLaterOutcomes(t *testing.T) {
	t.Setenv("MINI_RUNNER_HELPER", "results")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	env := helperRunEnv(t)
	outcomes, err := runSets(env, []scenarioSet{{Name: "errors", Scenarios: []string{"first", "broken", "last"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 3 || !outcomes[0].passed() || !outcomes[2].passed() ||
		!strings.Contains(outcomes[1].Error, "decode checks.json") {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	summary, err := os.ReadFile(
		filepath.Join(env.Results, "summary.md"),
	) //fileiolint:allow inspect cumulative summary after runner error
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summary), "2 of 3 scenarios passed") ||
		!strings.Contains(string(summary), "decode checks.json") {
		t.Fatalf("summary = %s", summary)
	}
}
