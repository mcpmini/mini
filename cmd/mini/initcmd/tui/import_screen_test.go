package tui

import (
	"fmt"
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

func importScreenFor(candidates []initcmd.Candidate) *importScreen {
	return newImportScreen(initcmd.ImportPlan{Candidates: candidates})
}

func screenText(s *importScreen) string {
	return ansi.Strip(s.heading() + "\n" + s.body(20, true))
}

func TestImportScreen_listsEachCandidateTickedAsThePlanPicks(t *testing.T) {
	s := importScreenFor([]initcmd.Candidate{
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
	text := screenText(importScreenFor([]initcmd.Candidate{second, switchedOff}))
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
	s := importScreenFor([]initcmd.Candidate{candidate("linear", "https://linear.example.com/mcp", true, "Cursor")})
	if text := screenText(s); !strings.HasPrefix(text, "Import servers from Cursor\n") ||
		strings.Contains(s.body(20, true), "Cursor") {
		t.Errorf("screen:\n%s\nwant Cursor named in the heading only", text)
	}
}

func TestImportScreen_ticksBecomeThePicks(t *testing.T) {
	candidates := []initcmd.Candidate{
		candidate("github", "https://gh.example.com/mcp", true, "Codex"),
		candidate("notes", "https://notes.example.com/mcp", false, "Codex"),
	}
	s := importScreenFor(candidates)
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

func TestImportScreen_enterAndSpaceTickTheRowAndDownPastItLeavesTheList(t *testing.T) {
	s := importScreenFor([]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")})
	for _, key := range []string{"enter", "space"} {
		if got, _ := s.handle(press(key)); got != handled {
			t.Errorf("%s = %v, want handled", key, got)
		}
	}
	if got := len(s.ticked()); got != 1 {
		t.Errorf("ticked %d after enter then space, want github ticked again", got)
	}
	for _, key := range []string{"esc", "left", "tab"} {
		if got, _ := s.handle(press(key)); got != unhandled {
			t.Errorf("%s = %v, want it left to the app", key, got)
		}
	}
	if got, _ := s.handle(press("down")); got != pastLastRow {
		t.Errorf("down on the last row = %v, want past the last row", got)
	}
}

func TestImportScreen_aLongCommandLeavesRoomForTheOtherColumns(t *testing.T) {
	long := initcmd.Candidate{
		Server: config.ServerConfig{Name: "files", Command: "npx", Args: []string{"-y", strings.Repeat("server-", 12)}},
		From:   []initcmd.AgentEntry{{Agent: "Codex", Name: "files"}},
	}
	other := candidate("notes", "https://notes.example.com/mcp", true, "Cursor")
	text := screenText(importScreenFor([]initcmd.Candidate{long, other}))
	if !strings.Contains(text, "…  Codex") {
		t.Errorf("screen:\n%s\nwant the command cut short so the agents column stays in line", text)
	}
}

func TestImportScreen_eachHeadingSitsAboveItsColumnEvenWhenTheColumnIsNarrower(t *testing.T) {
	s := importScreenFor([]initcmd.Candidate{
		candidate("a", "https://x.io", true, "Codex"),
		candidate("b", "https://y.io", true, "Cursor"),
	})
	lines := strings.Split(ansi.Strip(s.body(20, true)), "\n")
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
	s := importScreenFor(candidates)
	for range 4 {
		s.handle(press("down"))
	}
	lines := strings.Split(ansi.Strip(s.body(3, true)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "SERVER") ||
		!strings.HasPrefix(lines[2], "> [x] e") {
		t.Errorf("body at height 3:\n%s\nwant the header, then rows ending at the cursor on e",
			strings.Join(lines, "\n"))
	}
}

func withInMini(candidates []initcmd.Candidate, inMini ...initcmd.Candidate) *importScreen {
	plan := initcmd.ImportPlan{Candidates: candidates}
	for _, c := range inMini {
		plan.InMini = append(plan.InMini, initcmd.InMiniServer{Server: c.Server, From: c.From})
	}
	return newImportScreen(plan)
}

func TestImportScreen_serversMiniRunsFollowTheCandidatesMarkedAndCantBeTicked(t *testing.T) {
	s := withInMini(
		[]initcmd.Candidate{candidate("notes", "https://notes.example.com/mcp", true, "Codex")},
		candidate("github", "https://gh.example.com/mcp", false, "Codex", "Claude Code"),
	)
	want := "  SERVER      COMMAND / URL          FROM\n" +
		"> [x] notes   notes.example.com/mcp  Codex\n" +
		"   ✓  github  gh.example.com/mcp     Codex, Claude Code\n" +
		"\n" +
		"   ✓ already in mini"
	if text := ansi.Strip(s.body(20, true)); text != want {
		t.Fatalf("body:\n%s\nwant:\n%s", text, want)
	}
	if got, _ := s.handle(press("down")); got == pastLastRow {
		t.Fatal("down from notes left the list, want the cursor on github's row")
	}
	for _, key := range []string{"space", "a", "a"} {
		s.handle(press(key))
	}
	if picks := s.ticked(); len(picks) != 1 || picks[0].Name != "notes" {
		t.Errorf("ticked = %v after space on github then a twice, want only notes: github can't be ticked", picks)
	}
}

func TestImportScreen_theCursorScrollsThroughMoreServersMiniRunsThanFit(t *testing.T) {
	var inMini []initcmd.Candidate
	for i := range 12 {
		name := fmt.Sprintf("m%02d", i)
		inMini = append(inMini, candidate(name, "https://"+name+".example.com/mcp", false, "Codex"))
	}
	s := withInMini([]initcmd.Candidate{candidate("notes", "https://notes.example.com/mcp", true, "Codex")}, inMini...)
	for range len(inMini) {
		s.handle(press("down"))
	}
	if text := ansi.Strip(s.body(10, true)); !strings.Contains(text, ">  ✓  m11") ||
		!strings.Contains(text, "✓ already in mini") {
		t.Errorf("body with the cursor on the last row:\n%s\nwant m11 under the cursor and the legend below", text)
	}
	if got, _ := s.handle(press("down")); got != pastLastRow {
		t.Errorf("down from the last row = %v, want past the last row", got)
	}
}

func TestImportScreen_theFilterNarrowsTheServersMiniRunsToo(t *testing.T) {
	s := withInMini(
		[]initcmd.Candidate{candidate("notes", "https://notes.example.com/mcp", true, "Codex")},
		candidate("github", "https://gh.example.com/mcp", false, "Codex"),
	)
	for _, key := range []string{"/", "n", "o", "enter"} {
		s.handle(press(key))
	}
	if text := ansi.Strip(
		s.body(20, true),
	); strings.Contains(text, "github") ||
		strings.Contains(text, "already in mini") {
		t.Errorf("body with filter no:\n%s\nwant github and its legend hidden", text)
	}
	s.handle(press("esc"))
	for _, key := range []string{"/", "g", "i", "t", "enter"} {
		s.handle(press(key))
	}
	text := ansi.Strip(s.body(20, true))
	if strings.Contains(text, "notes") || !strings.Contains(text, ">  ✓  github") {
		t.Errorf("body with filter git:\n%s\nwant only github, under the cursor", text)
	}
}

func TestImportScreen_withOnlyServersMiniRunsIsSkipped(t *testing.T) {
	s := withInMini(nil, candidate("github", "https://gh.example.com/mcp", false, "Codex"))
	if !s.empty() {
		t.Error("Import is shown with nothing to tick; want it skipped like any screen with nothing to pick")
	}
}
