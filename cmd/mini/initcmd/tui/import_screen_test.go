package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/config"
)

func candidate(name, url string, picked bool, agents ...string) initcmd.Candidate {
	c := initcmd.Candidate{Server: config.ServerConfig{Name: name, Transport: "http", URL: url}, Picked: picked}
	for _, agent := range agents {
		c.From = append(c.From, initcmd.AgentEntry{Agent: agent, Name: name})
	}
	return c
}

func screenText(s *importScreen) string {
	return ansi.Strip(s.heading() + "\n" + s.body(20))
}

func TestImportScreen_listsEachCandidateTickedAsThePlanPicks(t *testing.T) {
	s := newImportScreen([]initcmd.Candidate{
		candidate("github", "https://gh.example.com/mcp", true, "Claude Code", "Codex"),
		candidate("github-2", "https://gh.example.com/mcp", false, "Codex"),
	})
	text := screenText(s)
	for _, want := range []string{
		"Import servers from your agents",
		"> [x] github    gh.example.com/mcp  Claude Code, Codex",
		"  [ ] github-2  gh.example.com/mcp  Codex",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("screen missing %q:\n%s", want, text)
		}
	}
}

func TestImportScreen_anUntickedRowSaysWhy(t *testing.T) {
	switchedOff := candidate("notes", "https://notes.example.com/mcp", false, "Codex")
	switchedOff.Reason = initcmd.SkipSwitchedOff
	second := candidate("github-2", "https://gh.example.com/mcp", false, "Cursor")
	second.Reason, second.SharesName = initcmd.SkipSecondConfig, "github"
	text := screenText(newImportScreen([]initcmd.Candidate{second, switchedOff}))
	for _, want := range []string{
		"github-2  gh.example.com/mcp     Cursor\n      another config named github\n",
		"notes     notes.example.com/mcp  Codex\n      switched off in Codex",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("screen missing %q:\n%s", want, text)
		}
	}
}

func TestImportScreen_oneAgentIsNamedInTheHeadingInsteadOfAColumn(t *testing.T) {
	s := newImportScreen([]initcmd.Candidate{candidate("linear", "https://linear.example.com/mcp", true, "Cursor")})
	if text := screenText(s); !strings.HasPrefix(text, "Import servers from Cursor\n") ||
		strings.Contains(s.body(20), "Cursor") {
		t.Errorf("screen:\n%s\nwant Cursor named in the heading only", text)
	}
}

func TestImportScreen_ticksBecomeThePicks(t *testing.T) {
	candidates := []initcmd.Candidate{
		candidate("github", "https://gh.example.com/mcp", true, "Codex"),
		candidate("notes", "https://notes.example.com/mcp", false, "Codex"),
	}
	s := newImportScreen(candidates)
	for _, key := range []string{"space", "down", "space"} {
		s.handle(press(key))
	}
	s.pick(candidates)
	if candidates[0].Picked || !candidates[1].Picked {
		t.Errorf("picks = %v, %v; want github unticked and notes ticked", candidates[0].Picked, candidates[1].Picked)
	}
	s.handle(press("a"))
	s.pick(candidates)
	if !candidates[0].Picked || !candidates[1].Picked {
		t.Errorf("after a: picks = %v, %v; want every server ticked", candidates[0].Picked, candidates[1].Picked)
	}
}

func TestImportScreen_enterContinuesAndEscGoesBack(t *testing.T) {
	s := newImportScreen([]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")})
	for key, want := range map[string]step{"enter": forward, "esc": back, "left": back, "space": stay} {
		if got := s.handle(press(key)); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
}

func TestImportScreen_aSecondConfigSaysWhatDiffersFromTheFirst(t *testing.T) {
	first := candidate("github", "https://gh.example.com/mcp", true, "Claude Code")
	first.Server.Headers = map[string]string{"X-Team": "one"}
	second := candidate("github-2", "https://gh.example.com/mcp", false, "Cursor")
	second.Reason, second.SharesName = initcmd.SkipSecondConfig, "github"
	second.Server.Headers = map[string]string{"X-Team": "two"}
	if text := screenText(newImportScreen([]initcmd.Candidate{first, second})); !strings.Contains(
		text, "another config named github: different headers",
	) {
		t.Errorf("screen:\n%s\nwant the second config's row to name the headers as the difference", text)
	}
}

func TestImportScreen_aLongCommandLeavesRoomForTheOtherColumns(t *testing.T) {
	long := initcmd.Candidate{
		Server: config.ServerConfig{Name: "files", Command: "npx", Args: []string{"-y", strings.Repeat("server-", 12)}},
		From:   []initcmd.AgentEntry{{Agent: "Codex", Name: "files"}},
	}
	other := candidate("notes", "https://notes.example.com/mcp", true, "Cursor")
	text := screenText(newImportScreen([]initcmd.Candidate{long, other}))
	if !strings.Contains(text, "…  Codex") {
		t.Errorf("screen:\n%s\nwant the command cut short so the agents column stays in line", text)
	}
}
