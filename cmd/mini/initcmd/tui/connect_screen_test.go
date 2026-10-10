package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
)

func namedAgents(names ...string) []agents.Agent {
	var list []agents.Agent
	for _, name := range names {
		list = append(list, agents.Agent{
			Name: name, ConfigPath: "/home/u/" + name + ".json", RemoveDisables: name == "Codex",
		})
	}
	return list
}

// fakePlan's check waits until the test releases it with the removals, or the screen cancels it.
type fakePlan struct {
	removable map[string][]string
	release   chan initcmd.Removals
	cancelled chan struct{}
}

func newFakePlan(removable map[string][]string) *fakePlan {
	return &fakePlan{removable: removable, release: make(chan initcmd.Removals, 1), cancelled: make(chan struct{}, 1)}
}

func (f *fakePlan) HasDuplicates(agent string) bool { return len(f.removable[agent]) > 0 }

func (f *fakePlan) Check(ctx context.Context) initcmd.Removals {
	select {
	case r := <-f.release:
		return r
	case <-ctx.Done():
		select {
		case f.cancelled <- struct{}{}:
		default: // a test reads one cancel at most; later ones, such as the cleanup's, aren't waited on
		}
		return initcmd.Removals{}
	}
}

func (f *fakePlan) checksPass(s *connectScreen, check tea.Cmd) {
	f.release <- initcmd.Removals{ByAgent: f.removable}
	s.update(check())
}

func noneAdded() []string { return nil }

func connectScreenFor(
	t *testing.T,
	plan *fakePlan,
	list []agents.Agent,
	withMini map[string]bool,
) (*connectScreen, tea.Cmd) {
	s := newConnectScreen(connectParams{
		agents:   list,
		withMini: withMini,
		plan:     func() (connectPlan, error) { return plan, nil },
		added:    noneAdded,
	})
	s.resize(200, 30)
	t.Cleanup(s.checks.cancelAndWait)
	return s, showScreen(s)
}

func framed(s screen, back bool) *app {
	screens := []screen{s}
	if back {
		screens = []screen{&fakeScreen{name: "Before"}, s}
	}
	a := &app{screens: screens, width: 200, height: 30}
	a.moveTo(len(screens) - 1)
	return a
}

func connectText(s *connectScreen) string {
	return framedText(framed(s, false))
}

func linesUnder(s *connectScreen, choice initcmd.ConnectChoice) string {
	i := slices.Index(s.options(), choice)
	if i < 0 {
		return ""
	}
	return ansi.Strip(strings.Join(s.choiceLines(i), "\n"))
}

func framedText(a *app) string {
	return ansi.Strip(a.body(20))
}

func pickedNames(s *connectScreen) []string {
	var names []string
	for _, agent := range s.picked() {
		names = append(names, agent.Name)
	}
	return names
}

func TestConnectScreen_withOneAgentOffersOnlyTheOptions(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude"), nil)
	want := "> Just connect mini\n    Claude: adding mini, leaving existing MCPs\n  I'll connect mini later"
	if text := connectText(s); s.heading() != "Connect mini to Claude" || text != want {
		t.Fatalf("%s\n%s\nwant the heading to name Claude, and:\n%s", s.heading(), text, want)
	}
	a := framed(s, false)
	if cmd := send(a, "enter"); cmd == nil || s.chosen != initcmd.ConnectOnly {
		t.Errorf("enter = %v, chosen %v; want the app to finish with Just connect", cmd, s.chosen)
	}
	if got := pickedNames(s); strings.Join(got, ",") != "Claude" {
		t.Errorf("picked = %v, want Claude", got)
	}
}

func TestConnectScreen_withSeveralAgentsConnectsOnlyTheTickedOnes(t *testing.T) {
	list := namedAgents("Claude", "Codex", "Cursor", "Windsurf")
	s, _ := connectScreenFor(t, newFakePlan(nil), list, map[string]bool{"Windsurf": true})
	text := connectText(s)
	marked := "[x] Cursor\n   ✓  Windsurf  already connected\n\n> Just connect mini"
	if !strings.Contains(text, marked) {
		t.Fatalf(
			"screen:\n%s\nwant Windsurf marked connected under the others and the cursor on the first option",
			text,
		)
	}
	a := framed(s, false)
	send(a, "up", "space", "up", "up", "enter")
	text = framedText(a)
	leaves := strings.Contains(linesUnder(s, initcmd.DontConnect), "Leaves Claude, Codex and Cursor as they are")
	if !strings.Contains(text, "> [ ] Codex\n") || !leaves {
		t.Errorf(
			"screen:\n%s\nwant the cursor on Codex, unticked by enter, and I'll connect mini later naming every agent it leaves",
			text,
		)
	}
	if cmd := send(a, "tab", "down", "enter"); cmd == nil || s.chosen != initcmd.DontConnect {
		t.Errorf(
			"enter on I'll connect mini later = %v, chosen %v; want the app to finish with I'll connect mini later",
			cmd,
			s.chosen,
		)
	}
	if got := pickedNames(s); strings.Join(got, ",") != "Claude,Cursor" {
		t.Errorf("picked = %v, want Claude and Cursor", got)
	}
}

func TestConnectScreen_justConnectingSaysWhatHappensToEachTickedAgent(t *testing.T) {
	list := namedAgents("Claude", "Codex")
	plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"gh"}})
	s, _ := connectScreenFor(t, plan, list, map[string]bool{"Claude": true})
	both := "    Claude: mini already connected, nothing changes\n" +
		"    Codex: adding mini, leaving existing MCPs"
	if text := linesUnder(s, initcmd.ConnectOnly); text != both {
		t.Fatalf("Just connect says:\n%s\nwant a line for each ticked agent:\n%s", text, both)
	}
	s.handle(press("space"))
	if text := linesUnder(s, initcmd.ConnectOnly); strings.Contains(text, "Codex:") {
		t.Errorf("screen with Codex unticked:\n%s\nwant no line for Codex: nothing happens to it", text)
	}
}

func TestConnectScreen_isEmptyWhenEveryAgentHasMiniAndNothingToRemove(t *testing.T) {
	list := namedAgents("Claude", "Codex")
	both := map[string]bool{"Claude": true, "Codex": true}
	if s, _ := connectScreenFor(t, newFakePlan(nil), list, both); !s.empty() {
		t.Error("Connect is shown although every agent already has mini and nothing to remove")
	}
	if s, _ := connectScreenFor(t, newFakePlan(map[string][]string{"Codex": {"files"}}), list, both); s.empty() {
		t.Error("Connect is skipped although Codex has an entry mini could replace")
	}
	if s, _ := connectScreenFor(t, newFakePlan(nil), nil, nil); !s.empty() {
		t.Error("Connect is shown with no agent to connect")
	}
}

func TestConnectScreen_leavingCancelsTheRunningChecks(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}})
	s, _ := connectScreenFor(t, plan, namedAgents("Claude"), nil)
	a := framed(s, true)
	if send(a, "esc"); a.at != 0 {
		t.Fatalf("at = %d after esc, want back on the screen before", a.at)
	}
	<-plan.cancelled
}

func TestConnectScreen_escWithNothingToGoBackToKeepsTheChecksRunning(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude"), nil)
	a := framed(s, false)
	send(a, "esc")
	plan.checksPass(s, check)
	if cmd := send(a, "enter"); cmd == nil || s.chosen != initcmd.ConnectAndRemove {
		t.Errorf("enter on removing after esc = %v, chosen %v; want the checks to have finished", cmd, s.chosen)
	}
}

func TestConnectScreen_leavingDoesNotWaitForTheChecksToStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stuck := &stuckPlan{unblock: make(chan struct{})}
		s := newConnectScreen(connectParams{
			agents: namedAgents("Claude"),
			plan:   func() (connectPlan, error) { return stuck, nil },
			added:  noneAdded,
		})
		showScreen(s)
		a := framed(s, true)
		left := make(chan int, 1)
		go func() {
			send(a, "esc")
			left <- a.at
		}()
		synctest.Wait()
		select {
		case at := <-left:
			if at != 0 {
				t.Errorf("at = %d after esc, want back on the screen before", at)
			}
		default:
			t.Error("esc waited for a check that hadn't stopped yet")
		}
		close(stuck.unblock)
		s.checks.cancelAndWait()
	})
}

// stuckPlan's check ignores cancellation, as a server that is slow to close does.
type stuckPlan struct{ unblock chan struct{} }

func (*stuckPlan) HasDuplicates(string) bool { return true }

func (p *stuckPlan) Check(context.Context) initcmd.Removals {
	<-p.unblock
	return initcmd.Removals{}
}

func TestConnectScreen_saysWhichMCPsWereAddedToMini(t *testing.T) {
	for _, tc := range []struct {
		name  string
		added []string
		want  string
	}{
		{"none", nil, ""},
		{"one", []string{"github"}, "1 MCP was added to mini: github"},
		{"several", []string{"asana", "github", "notion"}, "3 MCPs were added to mini: asana, github, notion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newConnectScreen(connectParams{
				agents: namedAgents("Claude", "Codex"),
				plan:   func() (connectPlan, error) { return newFakePlan(nil), nil },
				added:  func() []string { return tc.added },
			})
			t.Cleanup(s.checks.cancelAndWait)
			s.resize(200, 30)
			showScreen(s)
			text := connectText(s)
			if tc.want == "" {
				if strings.Contains(text, "added to mini") {
					t.Errorf("screen:\n%s\nwant no added line: nothing was added", text)
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
		agents: namedAgents("Claude", "Codex"),
		plan:   func() (connectPlan, error) { return newFakePlan(nil), nil },
		added:  func() []string { return names },
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

func TestConnectScreen_everyAgentFitsAnOrdinaryWindowWithItsChoices(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"github"}})
	list := namedAgents("Claude", "Codex", "Cursor", "Windsurf", "Zed", "Cline", "Goose")
	s := newConnectScreen(connectParams{
		agents:   list,
		withMini: map[string]bool{"Cline": true, "Goose": true},
		plan:     func() (connectPlan, error) { return plan, nil },
		added: func() []string {
			return []string{"asana", "atlassian", "context7", "github", "notion", "slack", "stripe"}
		},
	})
	t.Cleanup(s.checks.cancelAndWait)
	s.resize(80, 24)
	plan.checksPass(s, showScreen(s))
	a := framed(s, true)
	a.width, a.height = 80, 24
	view := shown(a)
	for _, agent := range list[:5] {
		if !strings.Contains(view, "[x] "+agent.Name) {
			t.Errorf("view at 80x24:\n%s\nwant every agent's row, %s's too", view, agent.Name)
		}
	}
}

func TestConnectScreen_theAddedNoteIsNeverCut(t *testing.T) {
	var names []string
	for i := range 30 {
		names = append(names, fmt.Sprintf("server-%02d", i))
	}
	s := newConnectScreen(connectParams{
		agents: namedAgents("Claude", "Codex"),
		plan:   func() (connectPlan, error) { return newFakePlan(nil), nil },
		added:  func() []string { return names },
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
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), map[string]bool{"Claude": true})
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
	s, _ := connectScreenFor(t, plan, namedAgents("Claude", "Codex", "Cursor", "Windsurf", "Zed"), nil)
	a := framed(s, false)
	send(a, "down")
	lines := strings.Split(ansi.Strip(a.body(7)), "\n")
	if len(lines) > 7 || lines[len(lines)-2] != "> I'll connect mini later" {
		t.Errorf("body at height 7:\n%s\nwant at most 7 lines ending with I'll connect mini later and its subtitle",
			strings.Join(lines, "\n"))
	}
}

func TestConnectScreen_anAgentAlreadyConnectedIsMarkedWithOnlyOneToConnect(t *testing.T) {
	plan := newFakePlan(nil)
	s := newConnectScreen(connectParams{
		agents:   namedAgents("Claude", "Codex"),
		withMini: map[string]bool{"Codex": true},
		plan:     func() (connectPlan, error) { return plan, nil },
		added:    noneAdded,
	})
	t.Cleanup(s.checks.cancelAndWait)
	s.resize(60, 30)
	plan.checksPass(s, showScreen(s))
	want := "   ✓  Codex  already connected\n\n> Just connect mini"
	if text := ansi.Strip(
		framed(s, false).body(8),
	); !strings.HasPrefix(text, want) ||
		s.heading() != "Connect mini to Claude" {
		t.Errorf(
			"%s\n%s\nwant the heading to name Claude, and Codex marked above the choices:\n%s",
			s.heading(),
			text,
			want,
		)
	}
	if s.keys() != "↑↓ move · tab choices" {
		t.Errorf("keys = %q, want no tick keys: Codex can't be ticked", s.keys())
	}
}

func TestConnectScreen_withNoAgentTickedTheConnectChoicesSayToTickOne(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude", "Codex"), nil)
	a := framed(s, false)
	send(a, "up", "a", "tab")
	want := "> Just connect mini\n  Tick the agents above to connect mini to them\n  I'll connect mini later"
	if text := framedText(a); !strings.Contains(text, want) {
		t.Fatalf("screen with nothing ticked:\n%s\nwant every choice, the connect one saying to tick an agent:\n%s",
			text, want)
	}
	if cmd := send(a, "enter"); cmd != nil || s.chosen == initcmd.ConnectOnly {
		t.Errorf(
			"enter on Just connect = %v, chosen %v; want the screen kept: there is no agent to connect",
			cmd,
			s.chosen,
		)
	}
	if cmd := send(a, "down", "enter"); cmd == nil || s.chosen != initcmd.DontConnect {
		t.Errorf("enter on later = %v, chosen %v; want the app to finish with I'll connect mini later", cmd, s.chosen)
	}
}

func TestConnectScreen_aLoneAgentLeftAfterGoingBackIsConnectedThoughItWasUnticked(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Codex": {"github"}})
	s, _ := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), map[string]bool{"Codex": true})
	a := framed(s, false)
	send(a, "up", "up", "space")
	delete(plan.removable, "Codex")
	s.refresh()
	if got := pickedNames(s); strings.Join(got, ",") != "Claude" || len(s.choices()) != 2 {
		t.Errorf("picked %v, choices %v once only Claude is left; want Claude, which has no checkbox to tick again",
			got, s.choices())
	}
}

func TestConnectScreen_aTicksEveryAgentAndBackFollowsTheChoices(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude", "Codex"), nil)
	a := framed(s, true)
	send(a, "up", "a")
	if got := pickedNames(s); len(got) != 0 {
		t.Errorf("picked after a = %v, want none: every agent was ticked", got)
	}
	send(a, "a")
	if got := pickedNames(s); strings.Join(got, ",") != "Claude,Codex" {
		t.Errorf("picked after a again = %v, want both", got)
	}
	if text := framedText(a); !strings.HasSuffix(text, "I'll connect mini later\n  Back") {
		t.Errorf("screen:\n%s\nwant Back under the last choice", text)
	}
	if send(a, "tab", "down", "down", "enter"); a.at != 0 {
		t.Errorf("at = %d after enter on Back, want the screen before", a.at)
	}
}

func TestConnectScreen_aWithOneAgentLeavesItTicked(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude"), nil)
	a := framed(s, false)
	if cmd := send(a, "a", "enter"); cmd == nil || strings.Join(pickedNames(s), ",") != "Claude" {
		t.Errorf("enter after a = %v, picked %v; want Just connect for Claude: one agent shows no checkbox to untick",
			cmd, pickedNames(s))
	}
}

func TestConnectScreen_upFromTheFirstChoiceReachesTheLastAgentAndTabReturns(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude", "Codex"), nil)
	a := framed(s, false)
	send(a, "up")
	if text := framedText(a); !strings.Contains(text, "> [x] Codex") {
		t.Fatalf("screen after up:\n%s\nwant the cursor on Codex, the last agent", text)
	}
	send(a, "tab")
	if text := framedText(a); !strings.Contains(text, "> Just connect mini") || strings.Contains(text, "> [x]") {
		t.Errorf("screen after tab:\n%s\nwant the cursor on the first choice", text)
	}
}
