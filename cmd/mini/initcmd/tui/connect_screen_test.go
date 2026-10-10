package tui

import (
	"context"
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
	want := "> Just connect mini\n  Adds mini next to your existing MCPs\n  Don't connect\n  Leaves Claude as it is"
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
	leaves := strings.Contains(text, "Leaves Claude, Codex and Cursor as they are")
	if !strings.Contains(text, "> [ ] Codex\n") || !leaves {
		t.Errorf(
			"screen:\n%s\nwant the cursor on Codex, unticked by enter, and Don't connect naming every agent it leaves",
			text,
		)
	}
	if cmd := send(a, "tab", "down", "enter"); cmd == nil || s.chosen != initcmd.DontConnect {
		t.Errorf("enter on Don't connect = %v, chosen %v; want the app to finish with Don't connect", cmd, s.chosen)
	}
	if got := pickedNames(s); strings.Join(got, ",") != "Claude,Cursor" {
		t.Errorf("picked = %v, want Claude and Cursor", got)
	}
}

func TestConnectScreen_justConnectingAnAgentThatHasMiniSaysItChangesNothing(t *testing.T) {
	list := namedAgents("Claude", "Codex")
	plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"gh"}})
	s, _ := connectScreenFor(t, plan, list, map[string]bool{"Claude": true})
	adds := "Just connect mini\n  Adds mini next to your existing MCPs"
	if text := connectText(s); !strings.Contains(text, adds) {
		t.Fatalf("screen:\n%s\nwant Just connect to add mini while Codex, which lacks it, is ticked", text)
	}
	s.handle(press("space"))
	changesNothing := "Just connect mini\n  Changes nothing: Claude already has a mini entry."
	if text := connectText(s); !strings.Contains(text, changesNothing) {
		t.Errorf("screen:\n%s\nwant Just connect to say it changes nothing once only Claude is ticked", text)
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

func TestConnectScreen_removingWaitsForTheChecksThenCountsTheTickedAgents(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files", "notes"}, "Codex": {"github"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
	if text := connectText(
		s,
	); !strings.Contains(
		text,
		"> Connect mini and remove existing MCPs\n  checking servers…",
	) {
		t.Fatalf("screen:\n%s\nwant removing first, highlighted, and checking", text)
	}
	a := framed(s, false)
	if cmd := send(a, "enter"); cmd != nil {
		t.Fatalf("enter on removing while the checks run = %v, want it to wait", cmd)
	}

	plan.checksPass(s, check)
	text := connectText(s)
	count := strings.Contains(text, "Removes 3 MCPs that mini now runs")
	if !count || !strings.Contains(text, "Codex: existing MCPs will be disabled, not removed") {
		t.Errorf("screen:\n%s\nwant 3 MCPs counted across both agents, and the Codex note", text)
	}
	send(a, "up", "space")
	text = framedText(a)
	if !strings.Contains(text, "Removes 2 MCPs") || strings.Contains(text, "Codex: existing") {
		t.Errorf("screen with Codex unticked:\n%s\nwant only Claude's 2 MCPs and no Codex note", text)
	}
	if cmd := send(a, "down", "enter"); cmd == nil || s.chosen != initcmd.ConnectAndRemove {
		t.Fatalf("enter on removing = %v, chosen %v; want the app to finish with removing", cmd, s.chosen)
	}
	if got := s.chosenConnect().Removals.ByAgent["Claude"]; strings.Join(got, ",") != "files,notes" {
		t.Errorf("removals passed on = %v, want Claude's files and notes", got)
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

func TestConnectScreen_aCheckRunFromAnEarlierVisitIsIgnored(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}})
	s, earlier := connectScreenFor(t, plan, namedAgents("Claude"), nil)
	later := showScreen(s)
	<-plan.cancelled
	s.update(earlier())
	if text := connectText(s); !strings.Contains(text, "checking servers…") {
		t.Fatalf("screen:\n%s\nwant the earlier visit's result ignored while this visit's checks run", text)
	}
	plan.checksPass(s, later)
	if text := connectText(s); !strings.Contains(text, "Removes 1 MCP that") {
		t.Errorf("screen:\n%s\nwant this visit's result counted", text)
	}
}

func TestConnectScreen_removingSaysWhatTheChecksLeft(t *testing.T) {
	t.Run("the Codex note shows only when Codex loses an entry", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
		plan.checksPass(s, check)
		if text := connectText(s); strings.Contains(text, "Codex: existing") {
			t.Errorf("screen:\n%s\nwant no Codex note: nothing in Codex is removed", text)
		}
	})
	t.Run("nothing passed", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude"), nil)
		plan.release <- initcmd.Removals{}
		s.update(check())
		want := "Nothing to remove: none of your MCPs work in mini yet"
		if text := connectText(s); !strings.Contains(text, want) {
			t.Errorf("screen:\n%s\nwant it to say nothing can be removed yet", text)
		}
	})
	t.Run("a narrow window wraps the subtitle", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude"), nil)
		s.resize(44, 30)
		plan.checksPass(s, check)
		for _, line := range strings.Split(connectText(s), "\n") {
			if ansi.StringWidth(line) > 44 {
				t.Errorf("line %q is wider than the 44-column window", line)
			}
		}
	})
}

func TestConnectScreen_removingWithTheRemovableAgentsUntickedSaysSo(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Codex": {"github"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
	plan.checksPass(s, check)
	s.handle(press("space"))
	text := connectText(s)
	if !strings.Contains(text, "Nothing to remove from the ticked agents") ||
		strings.Contains(text, "work in mini yet") {
		t.Errorf("screen:\n%s\nwant it to say the ticked agents have nothing to remove, not that checks failed", text)
	}
}

func TestConnectScreen_aShortWindowKeepsTheCursorsOptionInView(t *testing.T) {
	plan := newFakePlan(nil)
	s, _ := connectScreenFor(t, plan, namedAgents("Claude", "Codex", "Cursor", "Windsurf", "Zed"), nil)
	a := framed(s, false)
	send(a, "down")
	lines := strings.Split(ansi.Strip(a.body(7)), "\n")
	if len(lines) > 7 || lines[len(lines)-2] != "> Don't connect" {
		t.Errorf("body at height 7:\n%s\nwant at most 7 lines ending with Don't connect and its subtitle",
			strings.Join(lines, "\n"))
	}
}

func TestConnectScreen_anAgentAlreadyConnectedIsMarkedWithOnlyOneToConnect(t *testing.T) {
	plan := newFakePlan(nil)
	s := newConnectScreen(connectParams{
		agents:   namedAgents("Claude", "Codex"),
		withMini: map[string]bool{"Codex": true},
		plan:     func() (connectPlan, error) { return plan, nil },
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

func TestConnectScreen_withNoAgentTickedOffersOnlyDontConnect(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude", "Codex"), nil)
	a := framed(s, false)
	send(a, "up", "a", "tab")
	if got := s.choices(); len(got) != 1 || got[0] != optionLabel(initcmd.DontConnect) {
		t.Fatalf("choices with nothing ticked = %v, want only Don't connect: connecting would change nothing", got)
	}
	if cmd := send(a, "enter"); cmd == nil || s.chosen != initcmd.DontConnect {
		t.Errorf("enter = %v, chosen %v; want the app to finish with Don't connect", cmd, s.chosen)
	}
	send(a, "up", "space")
	if got := s.choices(); len(got) != 2 {
		t.Errorf("choices with Codex ticked again = %v, want Just connect back", got)
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
	if text := framedText(a); !strings.HasSuffix(text, "Leaves Claude and Codex as they are\n\n  Back") {
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
