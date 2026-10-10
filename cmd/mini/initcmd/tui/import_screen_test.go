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

func TestImportScreen_onlyASwitchedOffRowSaysWhyItIsUnticked(t *testing.T) {
	switchedOff := candidate("notes", "https://notes.example.com/mcp", false, "Codex")
	switchedOff.Reason = initcmd.SkipSwitchedOff
	second := candidate("github-2", "https://gh.example.com/mcp", false, "Cursor")
	second.Reason = initcmd.SkipSecondConfig
	text := screenText(newImportScreen([]initcmd.Candidate{second, switchedOff}))
	for _, want := range []string{
		"github-2  gh.example.com/mcp     Cursor\n  [ ] notes",
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

func TestImportScreen_enterTicksTheRowAndContinuesFromContinue(t *testing.T) {
	s := newImportScreen([]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")})
	for key, want := range map[string]step{"enter": stay, "esc": back, "left": back, "space": stay} {
		if got, _ := s.handle(press(key)); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if got := len(s.ticked()); got != 1 {
		t.Errorf("ticked %d after enter then space, want github ticked again", got)
	}
	s.handle(press("down"))
	if text := screenText(s); !strings.HasSuffix(text, "\n> Continue →") {
		t.Errorf("screen:\n%s\nwant the cursor on Continue after moving past the last row", text)
	}
	if got, _ := s.handle(press("enter")); got != forward {
		t.Errorf("enter on Continue = %v, want forward", got)
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

func TestImportScreen_eachHeadingSitsAboveItsColumnEvenWhenTheColumnIsNarrower(t *testing.T) {
	s := newImportScreen([]initcmd.Candidate{
		candidate("a", "https://x.io", true, "Codex"),
		candidate("b", "https://y.io", true, "Cursor"),
	})
	lines := strings.Split(ansi.Strip(s.body(20)), "\n")
	header, row := lines[0], lines[1]
	for heading, cell := range map[string]string{"SERVER": "[x]", "COMMAND / URL": "x.io", "FROM": "Codex"} {
		if strings.Index(header, heading) != strings.Index(row, cell) {
			t.Errorf("header %q and row %q: %s doesn't start where %s does", header, row, cell, heading)
		}
	}
}

func TestImportScreen_theHeaderStaysAboveTheRowsAsTheyScroll(t *testing.T) {
	var candidates []initcmd.Candidate
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		candidates = append(candidates, candidate(name, "https://"+name+".example.com/mcp", true, "Codex", "Cursor"))
	}
	s := newImportScreen(candidates)
	for range 4 {
		s.handle(press("down"))
	}
	lines := strings.Split(ansi.Strip(s.body(5)), "\n")
	if len(lines) != 5 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "SERVER") ||
		!strings.HasPrefix(lines[2], "> [x] e") {
		t.Errorf("body at height 5:\n%s\nwant the header, then rows ending at the cursor on e, then Continue",
			strings.Join(lines, "\n"))
	}
}
