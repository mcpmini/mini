//go:build test

package initcmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func agentNames(list []agents.Agent) []string {
	var names []string
	for _, a := range list {
		names = append(names, a.Name)
	}
	return names
}

func TestConnectableAgents(t *testing.T) {
	f := newApplyFixture(t)
	f.write(t, "Claude Code", `{"mcpServers":{}}`)
	f.write(t, "Cursor", `not json`)
	if err := os.MkdirAll(f.agents["Windsurf"].Dir, 0700); err != nil {
		t.Fatal(err)
	}
	known := []agents.Agent{f.agents["Claude Code"], f.agents["Codex"], f.agents["Cursor"], f.agents["Windsurf"]}

	got := agentNames(ConnectableAgents(known))

	if want := []string{"Claude Code", "Windsurf"}; !reflect.DeepEqual(got, want) {
		t.Errorf("connectable = %v, want %v: a config that reads, and an installed agent with none yet", got, want)
	}
}

func TestConnectableAgents_claudeCodeIsInstalledOnlyWithItsOwnDirectory(t *testing.T) {
	f := newApplyFixture(t)
	claude := []agents.Agent{f.agents["Claude Code"]}
	if got := agentNames(ConnectableAgents(claude)); got != nil {
		t.Errorf("connectable = %v, want none: its config would land in the home directory every user has", got)
	}

	if err := os.MkdirAll(filepath.Join(f.home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := agentNames(ConnectableAgents(claude)); !reflect.DeepEqual(got, []string{"Claude Code"}) {
		t.Errorf("connectable = %v, want Claude Code once ~/.claude exists", got)
	}
}

func TestMiniServersDuplicates(t *testing.T) {
	configDir := t.TempDir()
	configtest.WriteServer(t, configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	configtest.WriteServer(t, configDir, config.ServerConfig{Name: "off", Command: "off-server", Enabled: new(false)})
	mini, err := LoadMiniServers(configDir)
	if err != nil {
		t.Fatal(err)
	}
	stdio := func(command string) agents.Server {
		return agents.Server{Config: config.ServerConfig{Command: command}}
	}
	templated := stdio("files-server")
	templated.UnexpandableRefs = []string{"an environment variable in command or args"}
	entries := map[string]agents.Server{
		"fs":        stdio("files-server"),
		"mini":      stdio("files-server"),
		"templated": templated,
		"off-copy":  stdio("off-server"),
		"other":     stdio("other-server"),
	}

	got := mini.Duplicates(entries, testSelf)

	if want := map[string]string{"fs": "files"}; !reflect.DeepEqual(got, want) {
		t.Errorf("duplicates = %v, want %v", got, want)
	}
}

func TestMiniServersCheck(t *testing.T) {
	configDir := t.TempDir()
	configtest.WriteServer(t, configDir, config.ServerConfig{Name: "up", Command: "up-server"})
	configtest.WriteServer(t, configDir, config.ServerConfig{Name: "down", Command: "down-server"})
	configtest.WriteServer(t, configDir, config.ServerConfig{Name: "unasked", Command: "unasked-server"})
	mini, err := LoadMiniServers(configDir)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("connection refused")
	probe := func(_ context.Context, _ string, sc config.ServerConfig) error {
		if sc.Name == "down" {
			return failure
		}
		return nil
	}

	got := mini.Check(context.Background(), CheckParams{ConfigDir: configDir, Servers: []string{"up", "down", "missing"}, Clock: clock.System(), Probe: probe})

	if want := map[string]error{"up": nil, "down": failure}; !reflect.DeepEqual(got, want) {
		t.Errorf("checks = %v, want %v", got, want)
	}
}

func TestMiniServersCheck_hungServerTimesOut(t *testing.T) {
	configDir := t.TempDir()
	configtest.WriteServer(t, configDir, config.ServerConfig{Name: "hung", Command: "hung-server"})
	mini, err := LoadMiniServers(configDir)
	if err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFake()
	probe := func(ctx context.Context, _ string, _ config.ServerConfig) error {
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan map[string]error, 1)
	go func() {
		done <- mini.Check(context.Background(), CheckParams{ConfigDir: configDir, Servers: []string{"hung"}, Clock: fake, Probe: probe})
	}()

	if err := fake.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	fake.Advance(connectCheckTimeout)

	if got := <-done; !errors.Is(got["hung"], context.Canceled) {
		t.Errorf("checks = %v, want hung to fail once the check timeout passes", got)
	}
}
