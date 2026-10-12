package tui

import (
	"context"
	"errors"
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

func (f *fakePlan) DuplicatedServers(agents ...string) []string {
	var names []string
	for _, agent := range agents {
		names = append(names, f.removable[agent]...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

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

func noStatuses() ([]initcmd.ServerStatus, error) { return nil, nil }

func connectScreenFor(
	t *testing.T,
	plan *fakePlan,
	list []agents.Agent,
	withMini map[string]bool,
) (*connectScreen, tea.Cmd) {
	s := newConnectScreen(connectParams{
		saveFailures: noSaveFailures,
		agents:       list,
		withMini:     withMini,
		plan:         func() (connectPlan, error) { return plan, nil },
		added:        noneAdded,
		statuses:     noStatuses,
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

func TestConnectScreen_saysWhichServersTheSaveCouldNotSave(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude"), nil)
	s.p.saveFailures = func() []initcmd.ServerError {
		full := errors.New("disk full")
		return []initcmd.ServerError{{Name: "linear", Err: full}, {Name: "sentry", Err: full}}
	}
	s.refresh()
	if text := connectText(s); !strings.Contains(text, "✗ mini couldn't save linear, sentry: disk full") {
		t.Errorf("Connect:\n%s\nwant one line naming both servers the full disk stopped", text)
	}
}

func TestConnectScreen_withOneAgentOffersOnlyTheOptions(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude"), nil)
	want := "> Just connect mini\n" +
		"  Adds mini to your agent configurations and leaves existing MCP configurations alone.\n" +
		"  I'll connect mini later\n" +
		"  Leaves your agent configurations alone."
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

func TestConnectScreen_eachAgentRowNamesTheMCPsMiniWouldReplace(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"linear"}, "Codex": {"github", "sentry"}})
	withMini := map[string]bool{"Claude": true, "Windsurf": true}
	s, _ := connectScreenFor(t, plan, namedAgents("Claude", "Codex", "Cursor", "Windsurf"), withMini)
	want := "[x] Claude    mini already connected · also configured with linear\n" +
		"  [x] Codex     configured with github, sentry\n" +
		"  [x] Cursor\n" +
		"   ✓  Windsurf  mini already connected"
	if text := connectText(s); !strings.Contains(text, want) {
		t.Errorf("screen:\n%s\nwant:\n%s", text, want)
	}
	if !s.ticked["Claude"] {
		t.Error("Claude, which has mini, starts unticked; want it ticked so removing its linear is the default")
	}
}

func TestConnectScreen_withSeveralAgentsConnectsOnlyTheTickedOnes(t *testing.T) {
	list := namedAgents("Claude", "Codex", "Cursor", "Windsurf")
	s, _ := connectScreenFor(t, newFakePlan(nil), list, map[string]bool{"Windsurf": true})
	text := connectText(s)
	marked := "[x] Cursor\n   ✓  Windsurf  mini already connected\n\n> Just connect mini"
	if !strings.Contains(text, marked) {
		t.Fatalf(
			"screen:\n%s\nwant Windsurf marked connected under the others and the cursor on the first option",
			text,
		)
	}
	a := framed(s, false)
	send(a, "up", "space", "up", "up", "enter")
	text = framedText(a)
	saves := strings.Contains(
		linesUnder(s, initcmd.DontConnect),
		"Leaves your agent configurations alone.",
	)
	if !strings.Contains(text, "> [ ] Codex\n") || !saves {
		t.Errorf(
			"screen:\n%s\nwant the cursor on Codex, unticked by enter, and I'll connect mini later saying it leaves the configs alone",
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

func TestConnectScreen_justConnectingWarnsOnlyWhileATickedAgentHasDuplicates(t *testing.T) {
	list := namedAgents("Claude", "Codex")
	plan := newFakePlan(map[string][]string{"Claude": {"files"}})
	s, _ := connectScreenFor(t, plan, list, map[string]bool{"Claude": true})
	const adds = "  Adds mini to your agent configurations and leaves existing MCP configurations alone."
	const warns = "  ⚠ Your agents will see these MCPs twice and may not use mini for smaller responses"
	if text := linesUnder(s, initcmd.ConnectOnly); text != adds+"\n"+warns {
		t.Fatalf("Just connect says:\n%s\nwant the adding line and the duplicates warning:\n%s", text, adds+"\n"+warns)
	}
	s.handle(press("up"))
	s.handle(press("space"))
	if text := linesUnder(s, initcmd.ConnectOnly); text != adds {
		t.Errorf("screen with only Codex ticked:\n%s\nwant the adding line alone: Codex has no duplicates", text)
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
	if cmd := send(a, "up", "enter"); cmd == nil || s.chosen != initcmd.ConnectAndRemove {
		t.Errorf("enter on removing after esc = %v, chosen %v; want the checks to have finished", cmd, s.chosen)
	}
}

func TestConnectScreen_leavingDoesNotWaitForTheChecksToStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stuck := &stuckPlan{unblock: make(chan struct{})}
		s := newConnectScreen(connectParams{
			saveFailures: noSaveFailures,
			agents:       namedAgents("Claude"),
			plan:         func() (connectPlan, error) { return stuck, nil },
			statuses:     noStatuses,
			added:        noneAdded,
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

func (*stuckPlan) DuplicatedServers(...string) []string { return []string{"files"} }

func (p *stuckPlan) Check(context.Context) initcmd.Removals {
	<-p.unblock
	return initcmd.Removals{}
}

func TestConnectScreen_anAgentAlreadyConnectedIsMarkedWithOnlyOneToConnect(t *testing.T) {
	plan := newFakePlan(nil)
	s := newConnectScreen(connectParams{
		saveFailures: noSaveFailures,
		agents:       namedAgents("Claude", "Codex"),
		withMini:     map[string]bool{"Codex": true},
		plan:         func() (connectPlan, error) { return plan, nil },
		statuses:     noStatuses,
		added:        noneAdded,
	})
	t.Cleanup(s.checks.cancelAndWait)
	s.resize(60, 30)
	plan.checksPass(s, showScreen(s))
	want := "   ✓  Codex  mini already connected\n\n> Just connect mini"
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
	send(a, "up", "a", "tab", "up")
	want := "> Just connect mini\n  Tick the agents above to connect mini to them\n" +
		"  I'll connect mini later\n  Leaves your agent configurations alone."
	if text := framedText(a); !strings.Contains(text, want) {
		t.Fatalf(
			"screen with nothing ticked:\n%s\nwant the connect choices dimmed with the cursor still able to rest on them, and:\n%s",
			text,
			want,
		)
	}
	if cmd := send(a, "enter"); cmd != nil {
		t.Errorf("enter on Just connect mini with nothing ticked = %v, want nothing to happen", cmd)
	}
	send(a, "down")
	if !strings.Contains(a.render(), dim.Render("Just connect mini")) {
		t.Errorf("Just connect mini is not dim while nothing is ticked:\n%s", a.render())
	}
	if s.choose(0) {
		t.Errorf("choose(Just connect) = true with nothing ticked, want refused: there is no agent to connect")
	}
	if cmd := send(a, "enter"); cmd == nil || s.chosen != initcmd.DontConnect {
		t.Errorf("enter on later = %v, chosen %v; want the app to finish with I'll connect mini later", cmd, s.chosen)
	}
}

func TestConnectScreen_untickingEveryAgentDimsTheConnectChoicesUntilOneIsTickedAgain(t *testing.T) {
	s, _ := connectScreenFor(t, newFakePlan(nil), namedAgents("Claude", "Codex"), nil)
	a := framed(s, false)
	send(a, "up", "a")
	if view := a.render(); !strings.Contains(view, dim.Render("Just connect mini")) ||
		!strings.Contains(ansi.Strip(view), "Just connect mini\n  Tick the agents above to connect mini to them") {
		t.Fatalf(
			"view with every agent unticked:\n%s\nwant Just connect mini dim and still listed with its tick line",
			ansi.Strip(view),
		)
	}
	send(a, "tab")
	if text := framedText(a); !strings.Contains(text, "> I'll connect mini later") {
		t.Errorf("view after tab:\n%s\nwant tab to land on I'll connect mini later, the only choice enter takes", text)
	}
	send(a, "up", "up")
	if text := framedText(a); strings.Contains(text, "> I'll connect") || strings.Contains(text, "> Just") {
		t.Errorf(
			"view after up from the first choice:\n%s\nwant the cursor on the rows",
			text,
		)
	}
	send(a, "space")
	if text := framedText(a); !strings.Contains(text, "> [x] Codex") {
		t.Fatalf("view after tick:\n%s\nwant Codex ticked again", text)
	}
	send(a, "tab")
	if text := framedText(a); !strings.Contains(text, "> Just connect mini\n  Adds mini") {
		t.Errorf("view after ticking one again:\n%s\nwant Just connect mini enabled under the cursor", text)
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
	lastChoice := "  Leaves your agent configurations alone.\n  Back"
	if text := framedText(a); !strings.HasSuffix(text, lastChoice) {
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

func TestConnectScreen_twoTickedAgentsShowAllThreeChoicesAndTheirLinesAtOnce(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"github"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
	plan.checksPass(s, check)
	text := connectText(s)
	for _, want := range []string{
		"> Connect mini and remove existing MCPs",
		"  Adds mini to your agent configurations.",
		"    Claude: Removing files",
		"    Codex:  Disabling github",
		"  Just connect mini",
		"  Adds mini to your agent configurations and leaves existing MCP configurations alone.",
		"  I'll connect mini later",
		"  Leaves your agent configurations alone.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("screen:\n%s\nwant all three choices and their lines at once, missing %q", text, want)
		}
	}
}

func noSaveFailures() []initcmd.ServerError { return nil }
