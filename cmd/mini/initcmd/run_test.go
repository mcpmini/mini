//go:build test

package initcmd

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
)

func TestRun(t *testing.T) {
	t.Run("abandoning stops the OAuth checks before it reports", func(t *testing.T) {
		probe := newFakeProbe(true)
		setup := Setup{
			ConfigDir: t.TempDir(),
			Add:       []catalog.Entry{{Name: "notes", URL: "https://notes.example/mcp"}},
			Probe:     probe.probe,
		}
		run := setup.Start(Plan{Add: setup.Add})
		run.Save()
		waitStarted(t, probe)

		report := run.Abandon()

		if _, finished := probe.counts(); finished != 1 {
			t.Errorf("checks finished = %d, want 1: a check still running could write after the report", finished)
		}
		if len(report.Servers) != 1 || report.Servers[0].Readiness != MayNeedLogin {
			t.Errorf("servers = %+v, want notes marked as maybe needing a login", report.Servers)
		}
	})

	t.Run("finishing reports the agents as the connect left them", func(t *testing.T) {
		f := newApplyFixture(t)
		cursor := f.write(t, "Cursor", `{"mcpServers":{}}`)
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		setup := Setup{ConfigDir: f.configDir, AgentsToConnect: []agents.Agent{cursor}, SelfPath: self}
		run := setup.Start(Plan{})
		run.Save()

		report := run.Finish(
			context.Background(),
			ConnectParams{Agents: []agents.Agent{cursor}, Choice: ConnectOnly},
		)

		if !slices.ContainsFunc(report.Agents.MiniServes, func(a agents.Agent) bool { return a.Name == "Cursor" }) {
			t.Errorf(
				"agents = %+v, want Cursor serving mini: the report must read Cursor after the connect",
				report.Agents,
			)
		}
	})
}
