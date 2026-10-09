package initcmd

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

const testSelf = "/opt/mini/bin/mini-under-test"

type applyFixture struct {
	mini      string
	home      string
	configDir string
	agents    map[string]agents.Agent
}

func newApplyFixture(t *testing.T) applyFixture {
	t.Helper()
	t.Setenv("CODEX_HOME", "")
	f := applyFixture{home: t.TempDir(), configDir: t.TempDir(), agents: map[string]agents.Agent{}}
	for _, agent := range agents.Known(f.home) {
		f.agents[agent.Name] = agent
	}
	f.mini = filepath.Join(f.home, "bin", "mini")
	testutil.WriteFile(t, f.mini, "#!/bin/sh\n")
	if err := os.Chmod(f.mini, 0o700); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f applyFixture) servingMini() string {
	return `{"command":"` + f.mini + `","args":["--config","` + f.configDir + `","connect"]}`
}

func (f applyFixture) write(t *testing.T, agent, content string) agents.Agent {
	t.Helper()
	a := f.agents[agent]
	testutil.WriteFile(t, a.ConfigPath, content)
	return a
}

func (f applyFixture) miniEntry() agents.MiniEntry {
	return agents.MiniEntry{Command: f.mini, Args: []string{"--config", f.configDir, "connect"}}
}

func (f applyFixture) apply(choice ConnectChoice, checks map[string]error, list ...agents.Agent) []AgentResult {
	return f.applyWith(
		context.Background(),
		applyInput{
			agents:   list,
			choice:   choice,
			removals: Removals{Checks: checks, ByAgent: f.everyDuplicateCounted(list)},
		},
	)
}

func (f applyFixture) everyDuplicateCounted(list []agents.Agent) map[string][]string {
	mini, err := loadMiniServers(f.configDir)
	shown := map[string][]string{}
	for _, agent := range list {
		if entries, readErr := agent.Read(agent.ConfigPath); err == nil && readErr == nil {
			shown[agent.Name] = slices.Sorted(maps.Keys(mini.Duplicates(entries, testSelf)))
		}
	}
	return shown
}

type applyInput struct {
	agents       []agents.Agent
	choice       ConnectChoice
	removals     Removals
	miniOverride agents.MiniEntry
}

func (f applyFixture) applyWith(ctx context.Context, in applyInput) []AgentResult {
	if in.miniOverride.Command == "" {
		in.miniOverride = f.miniEntry()
	}
	return apply(ctx, applyParams{
		agents: in.agents, choice: in.choice, counted: in.removals.ByAgent,
		rule: replacementRule{
			mini:      miniEntryCheck{configDir: f.configDir, selfPath: testSelf},
			miniToAdd: in.miniOverride,
			checks:    in.removals.Checks,
		},
		now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
	})
}

func entryNamesIn(t *testing.T, agent agents.Agent) []string {
	t.Helper()
	servers, err := agent.Read(agent.ConfigPath)
	if err != nil {
		t.Fatalf("read %s: %v", agent.ConfigPath, err)
	}
	var names []string
	for name, s := range servers {
		if !s.Disabled {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// The acceptance example: github passes its check and leaves Claude Code; linear fails and
// stays; Codex's github uses other credentials, so it stays switched on.
func TestApply_removesOnlyVerifiedDuplicates(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(
		t,
		f.configDir,
		config.ServerConfig{
			Name:      "github",
			Transport: "http",
			URL:       "https://gh.example/mcp",
			Headers:   map[string]string{"Authorization": "Bearer ${GH_A}"},
		},
	)
	configtest.WriteServer(
		t,
		f.configDir,
		config.ServerConfig{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"},
	)
	claude := f.write(t, "Claude Code", `{"mcpServers":{
		"github":{"type":"http","url":"https://gh.example/mcp","headers":{"Authorization":"Bearer ${GH_A}"}},
		"linear":{"type":"http","url":"https://linear.example/mcp"}}}`)
	codex := f.write(
		t,
		"Codex",
		"[mcp_servers.github]\nurl = \"https://gh.example/mcp\"\nbearer_token_env_var = \"GH_B\"\n",
	)
	checks := map[string]error{"github": nil, "linear": errors.New("needs a login")}

	results := f.apply(ConnectAndRemove, checks, claude, codex)

	if got := entryNamesIn(t, claude); !reflect.DeepEqual(got, []string{"linear", "mini"}) {
		t.Errorf("Claude Code entries = %v, want linear and mini", got)
	}
	if got := entryNamesIn(t, codex); !reflect.DeepEqual(got, []string{"github", "mini"}) {
		t.Errorf("Codex entries switched on = %v, want github and mini", got)
	}
	wantKept := []KeptEntry{{Entry: "linear", Server: "linear", Err: checks["linear"]}}
	if !reflect.DeepEqual(results[0].Removed, []string{"github"}) || !reflect.DeepEqual(results[0].Kept, wantKept) {
		t.Errorf("Claude Code result = %+v, want github removed and linear kept", results[0])
	}
	for _, r := range results {
		if r.Err != nil || r.Backup == "" {
			t.Errorf("%s: err = %v, backup = %q; want an edit with a backup", r.Agent.Name, r.Err, r.Backup)
		}
	}
}

func TestApply_keepsDuplicatesWhenTheWrittenMiniWontServeThem(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	cursor := f.write(t, "Cursor", `{"mcpServers":{"files":{"command":"files-server"}}}`)

	results := f.applyWith(context.Background(), applyInput{
		agents: []agents.Agent{cursor}, choice: ConnectAndRemove,
		miniOverride: agents.MiniEntry{Command: f.mini, Args: []string{"connect"}},
		removals:     Removals{Checks: map[string]error{"files": nil}},
	})

	wantKept := []KeptEntry{{Entry: "files", Server: "files", Err: errMiniInactive}}
	if results[0].Removed != nil || !reflect.DeepEqual(results[0].Kept, wantKept) {
		t.Errorf("result = %+v, want files kept: the mini written runs the default config directory", results[0])
	}
	if got := entryNamesIn(t, cursor); !reflect.DeepEqual(got, []string{"files", "mini"}) {
		t.Errorf("entries = %v, want files and the new mini", got)
	}
}

func TestApply_reportsWhetherTheAgentEndsUpWithAServingMini(t *testing.T) {
	f := newApplyFixture(t)
	claude := f.write(
		t,
		"Claude Code",
		`{"mcpServers":{"mini":{"command":"/old/mini","args":["connect"],"disabled":true}}}`,
	)
	cursor := f.write(t, "Cursor", `{"mcpServers":{}}`)
	if err := os.MkdirAll(f.agents["Windsurf"].Dir, 0o700); err != nil {
		t.Fatal(err)
	}

	results := f.apply(ConnectOnly, nil, claude, cursor, f.agents["Windsurf"])

	for i, want := range []bool{false, true, true} {
		if results[i].MiniServes != want || results[i].Err != nil {
			t.Errorf(
				"%s: MiniServes = %v, err = %v; want %v",
				results[i].Agent.Name,
				results[i].MiniServes,
				results[i].Err,
				want,
			)
		}
	}
}

func TestApply_aFailedEditReportsNothingRemoved(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "lin", Command: "lin-server"})
	codex := f.write(
		t,
		"Codex",
		"[mcp_servers.lin]\ncommand = \"lin-server\"\n\n[mcp_servers]\nfiles = { command = \"files-server\" }\n",
	)
	before := testutil.ReadFile(t, codex.ConfigPath)

	results := f.apply(ConnectAndRemove, map[string]error{"files": nil, "lin": nil}, codex)

	if results[0].Err == nil || results[0].Removed != nil || results[0].Kept != nil {
		t.Errorf("result = %+v, want the edit's error and nothing reported removed or kept", results[0])
	}
	if after := testutil.ReadFile(t, codex.ConfigPath); string(after) != string(before) {
		t.Errorf("config changed:\n%s", after)
	}
}

func TestApply_disablesInCodex(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	codex := f.write(t, "Codex", "[mcp_servers.files]\ncommand = \"files-server\"\n")

	results := f.apply(ConnectAndRemove, map[string]error{"files": nil}, codex)

	data := string(testutil.ReadFile(t, codex.ConfigPath))
	if !strings.Contains(data, "[mcp_servers.files]\nenabled = false\n") ||
		!reflect.DeepEqual(results[0].Removed, []string{"files"}) {
		t.Errorf("result = %+v, config:\n%s\nwant files switched off", results[0], data)
	}
}

func TestApply_choices(t *testing.T) {
	setup := func(t *testing.T) (applyFixture, agents.Agent) {
		f := newApplyFixture(t)
		configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
		return f, f.write(t, "Cursor", `{"mcpServers":{"files":{"command":"files-server"}}}`)
	}
	t.Run("connect only keeps every entry", func(t *testing.T) {
		f, cursor := setup(t)
		f.apply(ConnectOnly, map[string]error{"files": nil}, cursor)
		if got := entryNamesIn(t, cursor); !reflect.DeepEqual(got, []string{"files", "mini"}) {
			t.Errorf("entries = %v, want files and mini", got)
		}
	})
	t.Run("don't connect leaves the file alone", func(t *testing.T) {
		f, cursor := setup(t)
		before := testutil.ReadFile(t, cursor.ConfigPath)
		if results := f.apply(DontConnect, map[string]error{"files": nil}, cursor); results != nil {
			t.Errorf("results = %+v, want none", results)
		}
		if after := testutil.ReadFile(t, cursor.ConfigPath); string(after) != string(before) {
			t.Errorf("config changed:\n%s", after)
		}
	})
}

func TestApply_neverReplaces(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "off", Command: "off-server", Enabled: new(false)})
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "unasked", Command: "unasked-server"})
	cursor := f.write(t, "Cursor", `{"mcpServers":{
		"switched-off":{"command":"files-server","disabled":true},
		"never-checked":{"command":"unasked-server"},
		"unchecked":{"command":"files-server","args":["--other"]},
		"disabled-in-mini":{"command":"off-server"},
		"old-mini":`+f.servingMini()+`}}`)

	results := f.apply(ConnectAndRemove, map[string]error{"files": nil, "off": nil}, cursor)

	want := []string{"disabled-in-mini", "never-checked", "old-mini", "unchecked"}
	if got := entryNamesIn(t, cursor); !reflect.DeepEqual(got, want) || results[0].Removed != nil {
		t.Errorf("entries = %v, removed = %v; want %v and nothing removed", got, results[0].Removed, want)
	}
	wantKept := []KeptEntry{{Entry: "never-checked", Server: "unasked", Err: errNotChecked}}
	if !reflect.DeepEqual(results[0].Kept, wantKept) {
		t.Errorf("kept = %+v, want %+v", results[0].Kept, wantKept)
	}
}

func TestApply_comparesWithServerFilesAsWritten(t *testing.T) {
	f := newApplyFixture(t)
	t.Setenv("GH_TOKEN", "synthetic")
	configtest.WriteServer(
		t,
		f.configDir,
		config.ServerConfig{
			Name:      "github",
			Transport: "http",
			URL:       "https://gh.example/mcp",
			Headers:   map[string]string{"Authorization": "Bearer ${GH_TOKEN}"},
		},
	)
	cursor := f.write(
		t,
		"Cursor",
		`{"mcpServers":{"gh":{"url":"https://gh.example/mcp","headers":{"Authorization":"Bearer ${env:GH_TOKEN}"}}}}`,
	)

	results := f.apply(ConnectAndRemove, map[string]error{"github": nil}, cursor)

	if !reflect.DeepEqual(results[0].Removed, []string{"gh"}) {
		t.Errorf("removed = %v, want gh, which matches github before ${GH_TOKEN} is expanded", results[0].Removed)
	}
}

func TestApply_createsAMissingConfig(t *testing.T) {
	f := newApplyFixture(t)
	windsurf := f.agents["Windsurf"]
	if err := os.MkdirAll(windsurf.Dir, 0o700); err != nil {
		t.Fatal(err)
	}

	results := f.apply(ConnectAndRemove, nil, windsurf)

	if !results[0].Created || results[0].Err != nil || results[0].Backup != "" {
		t.Fatalf("result = %+v, want the file created with no backup", results[0])
	}
	if got := entryNamesIn(t, windsurf); !reflect.DeepEqual(got, []string{"mini"}) {
		t.Errorf("entries = %v, want mini", got)
	}
}

func TestApply_aFailedCreateReportsNoServingMini(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into read-only directories")
	}
	f := newApplyFixture(t)
	windsurf := f.agents["Windsurf"]
	if err := os.MkdirAll(windsurf.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(windsurf.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(windsurf.Dir, 0o700) }) //nolint:errcheck // only lets the temp dir be removed

	results := f.apply(ConnectOnly, nil, windsurf)

	if results[0].Err == nil || results[0].Created || results[0].MiniServes {
		t.Errorf("result = %+v, want the error and no mini reported: nothing was written", results[0])
	}
}

func TestApply_aConfigTheAgentWritesMeanwhileIsEdited(t *testing.T) {
	f := newApplyFixture(t)
	windsurf := f.agents["Windsurf"]
	if err := os.MkdirAll(windsurf.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	connect, wrote := windsurf.Connect, false
	windsurf.Connect = func(config []byte, remove []string, mini *agents.MiniEntry) ([]byte, error) {
		if !wrote {
			wrote = true
			testutil.WriteFile(t, windsurf.ConfigPath, `{"mcpServers":{"notes":{"command":"notes-server"}}}`)
		}
		return connect(config, remove, mini)
	}

	results := f.apply(ConnectOnly, nil, windsurf)

	if results[0].Err != nil || results[0].Created || results[0].Backup == "" {
		t.Errorf("result = %+v, want the agent's new file edited with a backup", results[0])
	}
	if got := entryNamesIn(t, windsurf); !reflect.DeepEqual(got, []string{"mini", "notes"}) {
		t.Errorf("entries = %v, want the agent's notes kept and mini added", got)
	}
}

func TestApply_reportsEntriesChangedSinceTheCheck(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "lin", Command: "lin-server"})
	cursor := f.write(
		t,
		"Cursor",
		`{"mcpServers":{"files":{"command":"files-server","args":["--edited"]},"lin":{"command":"lin-server"}}}`,
	)

	results := f.applyWith(context.Background(), applyInput{
		agents: []agents.Agent{cursor}, choice: ConnectAndRemove,
		removals: Removals{
			Checks:  map[string]error{"files": nil, "lin": errors.New("unreachable")},
			ByAgent: map[string][]string{"Cursor": {"files", "lin"}},
		},
	})

	if !reflect.DeepEqual(results[0].Changed, []string{"files"}) || results[0].Removed != nil {
		t.Errorf("result = %+v, want files reported as changed and lin, kept by its failed check, not", results[0])
	}
}

func TestApply_failuresAndCancel(t *testing.T) {
	t.Run("a failed agent doesn't stop the next", func(t *testing.T) {
		f := newApplyFixture(t)
		broken := f.write(t, "Cursor", `{"mcpServers": not json`)
		claude := f.write(t, "Claude Code", `{}`)
		results := f.apply(ConnectOnly, nil, broken, claude)
		if results[0].Err == nil || results[1].Err != nil {
			t.Errorf("errors = %v, %v; want only Cursor failed", results[0].Err, results[1].Err)
		}
	})
	t.Run("after cancel no agent is edited", func(t *testing.T) {
		f := newApplyFixture(t)
		claude := f.write(t, "Claude Code", `{}`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		results := f.applyWith(ctx, applyInput{agents: []agents.Agent{claude}, choice: ConnectOnly})
		if !errors.Is(results[0].Err, context.Canceled) || string(testutil.ReadFile(t, claude.ConfigPath)) != "{}" {
			t.Errorf("result = %+v, want cancelled and untouched", results[0])
		}
	})
}
