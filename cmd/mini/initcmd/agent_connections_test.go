//go:build test

package initcmd

import (
	"os"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

func TestExistingMini_UnknownDefaultConfigIsInactive(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	check := miniEntryCheck{configDir: t.TempDir(), selfPath: executable}
	entry := agents.Server{Config: config.ServerConfig{Command: executable, Args: []string{"connect"}}}
	if got := check.existingMini(map[string]agents.Server{"mini": entry}); got != MiniEntryInactive {
		t.Fatalf("existingMini = %v, want inactive when its implicit config directory is unknown", got)
	}
}

func TestClassifyAgents_byTheirMiniEntry(t *testing.T) {
	f := newApplyFixture(t)
	cursor := f.write(
		t,
		"Cursor",
		`{"mcpServers":{"mini":{"command":"/opt/old/mini","args":["connect"],"disabled":true}}}`,
	)
	windsurf := f.write(t, "Windsurf", `{"mcpServers":{"proxy":`+f.servingMini()+`}}`)
	claude := f.write(t, "Claude Code", `{"mcpServers":{}}`)

	c := ClassifyAgents(f.configDir, testSelf, []agents.Agent{cursor, windsurf, claude})

	for _, group := range []struct {
		name string
		got  []agents.Agent
		want []string
	}{
		{"unconnected", c.NoMini, []string{"Claude Code"}},
		{"has a serving mini", c.MiniServes, []string{"Windsurf"}},
		{"has an inactive mini", c.MiniInactive, []string{"Cursor"}},
	} {
		if got := agentNames(group.got); !reflect.DeepEqual(got, group.want) {
			t.Errorf("%s = %v, want %v", group.name, got, group.want)
		}
	}
}
