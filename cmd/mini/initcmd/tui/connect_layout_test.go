package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

func TestConnectScreen_saysWhichMCPsWereAddedToMiniAndLaterMentionsSavingOnlyThen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		added []string
		want  string
	}{
		{"none", nil, ""},
		{"one", []string{"github"}, "1 MCP will be added to mini: github"},
		{"several", []string{"asana", "github", "notion"}, "3 MCPs will be added to mini: asana, github, notion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newConnectScreen(connectParams{
				saveFailures: noSaveFailures,
				agents:       namedAgents("Claude", "Codex"),
				plan:         func() (connectPlan, error) { return newFakePlan(nil), nil },
				statuses:     noStatuses,
				added:        func() []string { return tc.added },
			})
			t.Cleanup(s.checks.cancelAndWait)
			s.resize(200, 30)
			showScreen(s)
			text := connectText(s)
			later := "Saves the MCPs to mini and leaves your agent configurations alone."
			if tc.want == "" {
				later = "Leaves your agent configurations alone."
			}
			if !strings.Contains(linesUnder(s, initcmd.DontConnect), later) {
				t.Errorf("lines under later:\n%s\nwant %q", linesUnder(s, initcmd.DontConnect), later)
			}
			if tc.want == "" {
				if strings.Contains(text, "added to mini") {
					t.Errorf("screen:\n%s\nwant no added line: nothing will be added", text)
				}
				return
			}
			if !strings.HasPrefix(text, tc.want+"\n\n") {
				t.Errorf("screen:\n%s\nwant it to start with %q and a blank line", text, tc.want)
			}
		})
	}
}

func TestConnectScreen_theAddedMCPsWrapWithinTheWindow(t *testing.T) {
	var names []string
	for i := range 12 {
		names = append(names, fmt.Sprintf("server-%02d", i))
	}
	s := newConnectScreen(connectParams{
		saveFailures: noSaveFailures,
		agents:       namedAgents("Claude", "Codex"),
		plan:         func() (connectPlan, error) { return newFakePlan(nil), nil },
		statuses:     noStatuses,
		added:        func() []string { return names },
	})
	t.Cleanup(s.checks.cancelAndWait)
	s.resize(minWidth, 30)
	showScreen(s)
	text := ansi.Strip(s.body(30, false))
	for _, line := range strings.Split(text, "\n") {
		if ansi.StringWidth(line) > minWidth {
			t.Errorf("line %q is wider than the %d-column window", line, minWidth)
		}
	}
	if !strings.Contains(strings.ReplaceAll(text, "\n", " "), "server-11") {
		t.Errorf("body:\n%s\nwant every added MCP listed", text)
	}
}

func TestConnectScreen_atAnOrdinaryWindowTheRowsOrTheCursorsChoiceAreInView(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"github"}})
	list := namedAgents("Claude", "Codex", "Cursor", "Windsurf", "Zed", "Cline", "Goose")
	s := newConnectScreen(connectParams{
		saveFailures: noSaveFailures,
		agents:       list,
		withMini:     map[string]bool{"Cline": true, "Goose": true},
		plan:         func() (connectPlan, error) { return plan, nil },
		statuses:     noStatuses,
		added: func() []string {
			return []string{"asana", "atlassian", "context7", "github", "notion", "slack", "stripe"}
		},
	})
	t.Cleanup(s.checks.cancelAndWait)
	s.resize(80, 24)
	plan.checksPass(s, showScreen(s))
	a := framed(s, true)
	a.width, a.height = 80, 24
	send(a, "up")
	view := shown(a)
	for _, agent := range list {
		if !strings.Contains(view, agent.Name) {
			t.Errorf(
				"view at 80x24 with the cursor on the rows:\n%s\nwant every agent listed, %s's too",
				view,
				agent.Name,
			)
		}
	}
	send(a, "tab")
	view = shown(a)
	for _, line := range []string{
		"> Connect mini and remove existing MCPs",
		"  Your existing configurations will be backed up first.",
		"    Claude: Removing files",
		"    Codex:  Disabling github",
	} {
		if !strings.Contains(view, line) {
			t.Errorf(
				"view at 80x24 with the cursor on the first choice:\n%s\nwant it and all its lines, missing %q",
				view,
				line,
			)
		}
	}
}

func TestConnectScreen_theAddedNoteIsNeverCut(t *testing.T) {
	var names []string
	for i := range 30 {
		names = append(names, fmt.Sprintf("server-%02d", i))
	}
	s := newConnectScreen(connectParams{
		saveFailures: noSaveFailures,
		agents:       namedAgents("Claude", "Codex"),
		plan:         func() (connectPlan, error) { return newFakePlan(nil), nil },
		statuses:     noStatuses,
		added:        func() []string { return names },
	})
	t.Cleanup(s.checks.cancelAndWait)
	s.resize(minWidth, 30)
	showScreen(s)
	body := ansi.Strip(s.body(4, true))
	if !strings.Contains(body, "server-29") || strings.Contains(body, "…") {
		t.Errorf("body with 4 lines to fill:\n%s\nwant every added MCP, wrapped, none cut", body)
	}
	a := framed(s, true)
	a.width, a.height = minWidth, minHeight
	if view := shown(a); strings.Count(view, "\n")+1 != minHeight || !strings.Contains(footerOf(view), "ctrl+c") {
		t.Errorf(
			"view at %dx%d:\n%s\nwant it to fill the window with the footer still at the bottom",
			minWidth,
			minHeight,
			view,
		)
	}
}

func TestConnectScreen_anAgentAlreadyConnectedGetsNoLineUnderTheChoices(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Codex": {"github"}})
	s, check := connectScreenFor(
		connectScreenForParams{
			T:        t,
			Plan:     plan,
			Agents:   namedAgents("Claude", "Codex"),
			WithMini: map[string]bool{"Claude": true},
		},
	)
	plan.checksPass(s, check)
	if text := linesUnder(
		s,
		initcmd.ConnectAndRemove,
	) + linesUnder(
		s,
		initcmd.ConnectOnly,
	); strings.Contains(
		text,
		"Claude",
	) {
		t.Errorf(
			"lines under the choices:\n%s\nwant none for Claude: its row says it's connected and nothing changes it",
			text,
		)
	}
}

func TestConnectScreen_aShortWindowKeepsTheCursorsOptionInView(t *testing.T) {
	plan := newFakePlan(nil)
	s, _ := connectScreenFor(
		connectScreenForParams{
			T:        t,
			Plan:     plan,
			Agents:   namedAgents("Claude", "Codex", "Cursor", "Windsurf", "Zed"),
			WithMini: nil,
		},
	)
	a := framed(s, false)
	send(a, "down")
	lines := strings.Split(ansi.Strip(a.body(7)), "\n")
	if len(lines) > 7 || lines[len(lines)-2] != "> I'll connect mini later" {
		t.Errorf("body at height 7:\n%s\nwant at most 7 lines ending with I'll connect mini later and its subtitle",
			strings.Join(lines, "\n"))
	}
}
