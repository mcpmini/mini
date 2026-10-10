package initcmd

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestApply_existingMiniEntry(t *testing.T) {
	t.Run("with nothing to remove the file is left alone", func(t *testing.T) {
		f := newApplyFixture(t)
		original := "[mcp_servers.mini]\ncommand = \"/old/mini\"\nargs = [\"connect\"]\nenabled = false\ntool_timeout_sec = 300\n"
		codex := f.write(t, "Codex", original)
		cursorOriginal := `{"mcpServers": {"mini": {"command": "/old/mini"}}}`
		cursor := f.write(t, "Cursor", cursorOriginal)
		results := f.apply(ConnectAndRemove, nil, codex, cursor)
		for i, want := range []string{original, cursorOriginal} {
			if results[i].ExistingMini != MiniEntryInactive || results[i].Backup != "" || results[i].Err != nil {
				t.Errorf(
					"result = %+v, want the switched-off or non-mini entry reported inactive and no edit",
					results[i],
				)
			}
			if got := string(testutil.ReadFile(t, results[i].Agent.ConfigPath)); got != want {
				t.Errorf("config:\n%s\nwant it unchanged", got)
			}
		}
	})
	t.Run("duplicates are still removed and mini's settings kept", func(t *testing.T) {
		f := newApplyFixture(t)
		configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
		cursor := f.write(t, "Cursor", `{"mcpServers":{"files":{"command":"files-server"},`+
			`"mini":{"command":"`+f.mini+`","args":["--config","`+f.configDir+`","connect"],"env":{"MINI_FLAG":"1"}}}}`)
		results := f.apply(ConnectAndRemove, map[string]error{"files": nil}, cursor)
		data := string(testutil.ReadFile(t, cursor.ConfigPath))
		if !reflect.DeepEqual(results[0].Removed, []string{"files"}) || !strings.Contains(data, f.mini) ||
			!strings.Contains(data, "MINI_FLAG") {
			t.Errorf("result = %+v, config:\n%s\nwant files removed and mini's entry unchanged", results[0], data)
		}
	})
	t.Run("mini under another key gets no second entry", func(t *testing.T) {
		f := newApplyFixture(t)
		configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
		codex := f.write(t, "Codex", "[mcp_servers.files]\ncommand = \"files-server\"\n\n"+
			"[mcp_servers.proxy]\ncommand = \""+f.mini+"\"\nargs = [\"--config="+f.configDir+"\", \"connect\"]\n")
		cursor := f.write(t, "Cursor", `{"mcpServers":{"proxy":`+f.servingMini()+`}}`)
		results := f.apply(ConnectAndRemove, map[string]error{"files": nil}, codex, cursor)
		if results[0].ExistingMini != MiniEntryServes || !reflect.DeepEqual(results[0].Removed, []string{"files"}) {
			t.Errorf("Codex result = %+v, want mini reported as connected and files switched off", results[0])
		}
		if data := string(testutil.ReadFile(t, codex.ConfigPath)); strings.Contains(data, "mcp_servers.mini") {
			t.Errorf("Codex config:\n%s\nwant no second mini entry", data)
		}
		if got := entryNamesIn(t, cursor); !reflect.DeepEqual(got, []string{"proxy"}) || results[1].Backup != "" {
			t.Errorf("Cursor entries = %v, result = %+v; want proxy alone and no edit", got, results[1])
		}
	})
	t.Run("a switched-off mini keeps the duplicates", func(t *testing.T) {
		f := newApplyFixture(t)
		configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
		mini := `{"command":"` + f.mini + `","args":["--config","` + f.configDir + `","connect"],"disabled":true}`
		cursor := f.write(t, "Cursor", `{"mcpServers":{"files":{"command":"files-server"},"mini":`+mini+`}}`)

		results := f.apply(ConnectAndRemove, map[string]error{"files": nil}, cursor)

		if results[0].ExistingMini != MiniEntryInactive || results[0].Removed != nil {
			t.Errorf("result = %+v, want the switched-off mini reported inactive and files kept", results[0])
		}
	})
	t.Run("a relative --config keeps the duplicates", func(t *testing.T) {
		f := newApplyFixture(t)
		configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
		t.Chdir(filepath.Dir(f.configDir))
		cursor := f.write(t, "Cursor", `{"mcpServers":{"files":{"command":"files-server"},`+
			`"mini":{"command":"`+f.mini+`","args":["--config","`+filepath.Base(f.configDir)+`","connect"]}}}`)

		results := f.apply(ConnectAndRemove, map[string]error{"files": nil}, cursor)

		if results[0].ExistingMini != MiniEntryInactive || results[0].Removed != nil {
			t.Errorf(
				"result = %+v, want the relative config dir reported inactive: the agent resolves it from its own directory",
				results[0],
			)
		}
	})
	t.Run("a mini entry that can't start keeps the duplicates", func(t *testing.T) {
		f := newApplyFixture(t)
		configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
		dir := `"--config","` + f.configDir + `"`
		cursor := f.write(t, "Cursor", `{"mcpServers":{"files":{"command":"files-server"},`+
			`"mini":{"command":"`+f.mini+`","args":[`+dir+`,"serve"]}}}`)
		claude := f.write(t, "Claude Code", `{"mcpServers":{"files":{"command":"files-server"},`+
			`"mini":{"command":"`+filepath.Join(f.home, "moved", "mini")+`","args":[`+dir+`,"connect"]}}}`)
		t.Setenv("PATH", filepath.Dir(f.mini))
		windsurf := f.write(t, "Windsurf", `{"mcpServers":{"files":{"command":"files-server"},`+
			`"mini":{"command":"mini","args":[`+dir+`,"connect"]}}}`)

		results := f.apply(ConnectAndRemove, map[string]error{"files": nil}, cursor, claude, windsurf)

		for _, result := range results {
			if result.ExistingMini != MiniEntryInactive || result.Removed != nil {
				t.Errorf(
					"%s result = %+v, want an old serve entry, a moved binary or a bare name on init's PATH reported inactive and files kept",
					result.Agent.Name,
					result,
				)
			}
		}
	})
	t.Run("a mini entry that won't serve the checked servers keeps their duplicates", func(t *testing.T) {
		f := newApplyFixture(t)
		configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
		codex := f.write(t, "Codex", "[mcp_servers.files]\ncommand = \"files-server\"\n\n"+
			"[mcp_servers.mini]\ncommand = \""+f.mini+"\"\nargs = [\"--config\", \""+f.configDir+"\", \"connect\"]\nenabled = false\n")
		cursor := f.write(t, "Cursor", `{"mcpServers":{"files":{"command":"files-server"},`+
			`"other-mini":{"command":"`+f.mini+`","args":["--config","/srv/other-mini","connect"]}}}`)
		before := [][]byte{testutil.ReadFile(t, codex.ConfigPath), testutil.ReadFile(t, cursor.ConfigPath)}

		results := f.applyWith(context.Background(), applyInput{
			agents: []agents.Agent{codex, cursor},
			choice: ConnectAndRemove,
			removals: Removals{
				Checks:  map[string]error{"files": nil},
				ByAgent: map[string][]string{"Codex": {"files"}, "Cursor": {"files"}},
			},
		})

		wantKept := []KeptEntry{{Entry: "files", Server: "files", Err: errMiniInactive}}
		for i, result := range results {
			if result.ExistingMini != MiniEntryInactive || result.Removed != nil ||
				!reflect.DeepEqual(result.Kept, wantKept) {
				t.Errorf("%s result = %+v, want files kept because mini won't serve it", result.Agent.Name, result)
			}
			if result.Changed != nil {
				t.Errorf(
					"%s changed = %v, want none: files is as Connect counted it",
					result.Agent.Name,
					result.Changed,
				)
			}
			if after := testutil.ReadFile(t, result.Agent.ConfigPath); string(after) != string(before[i]) {
				t.Errorf("%s config changed:\n%s", result.Agent.Name, after)
			}
		}
	})
}

func TestApply_aMiniServerChangedAfterItsCheckRemovesNoEntryConnectDidntCount(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "server-a"})
	cursor := f.write(t, "Cursor", `{"mcpServers":{"old":{"command":"server-a"},"other":{"command":"server-b"}}}`)
	counted := f.everyDuplicateCounted([]agents.Agent{cursor})
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "server-b"})

	results := f.applyWith(context.Background(), applyInput{
		agents: []agents.Agent{cursor}, choice: ConnectAndRemove,
		removals: Removals{Checks: map[string]error{"files": nil}, ByAgent: counted},
	})

	wantKept := []KeptEntry{{Entry: "other", Server: "files", Err: errNotCounted}}
	if results[0].Removed != nil || !reflect.DeepEqual(results[0].Kept, wantKept) {
		t.Errorf("result = %+v, want other kept: Connect counted only old", results[0])
	}
	if got := entryNamesIn(t, cursor); !reflect.DeepEqual(got, []string{"mini", "old", "other"}) {
		t.Errorf("entries = %v, want both kept and mini added", got)
	}
}
