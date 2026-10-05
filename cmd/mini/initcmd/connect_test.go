package initcmd

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
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
	if err := os.MkdirAll(f.agents["Gemini CLI"].Dir, 0700); err != nil {
		t.Fatal(err)
	}
	known := []agents.Agent{f.agents["Claude Code"], f.agents["Codex"], f.agents["Cursor"], f.agents["Gemini CLI"]}

	got := agentNames(ConnectableAgents(known))

	if want := []string{"Claude Code", "Gemini CLI"}; !reflect.DeepEqual(got, want) {
		t.Errorf("connectable = %v, want %v: a config that reads, and an installed agent with none yet", got, want)
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

func TestUnexpandedServer_keepsReferences(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("API_KEY", "synthetic")
	testutil.WriteFile(t, config.ServerPath(configDir, "svc"), "transport: http\nurl: https://example.com/mcp\nheaders:\n  X-Api-Key: ${API_KEY}\n")
	sc, err := UnexpandedServer(configDir, "svc")
	if err != nil || sc.Name != "svc" || sc.Headers["X-Api-Key"] != "${API_KEY}" {
		t.Errorf("UnexpandedServer = %+v, %v; want the reference as written", sc, err)
	}
}
