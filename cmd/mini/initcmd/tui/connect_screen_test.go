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

func connectText(s *connectScreen) string {
	return ansi.Strip(s.body(20))
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
	want := "> Just connect mini\n    Adds mini next to your existing MCPs\n  Don't connect\n    Leaves Claude as it is"
	if text := connectText(s); s.heading() != "Connect mini to Claude" || text != want {
		t.Fatalf("%s\n%s\nwant the heading to name Claude, and:\n%s", s.heading(), text, want)
	}
	if move, _ := s.handle(press("enter")); move != forward || s.chosen != initcmd.ConnectOnly {
		t.Errorf("enter = %v, chosen %v; want forward with Just connect", move, s.chosen)
	}
	if got := pickedNames(s); strings.Join(got, ",") != "Claude" {
		t.Errorf("picked = %v, want Claude", got)
	}
}

func TestConnectScreen_withSeveralAgentsConnectsOnlyTheTickedOnes(t *testing.T) {
	list := namedAgents("Claude", "Codex", "Cursor", "Windsurf")
	s, _ := connectScreenFor(t, newFakePlan(nil), list, map[string]bool{"Windsurf": true})
	text := connectText(s)
	noted := strings.HasPrefix(text, "Windsurf already has a mini entry.\n\n")
	if !noted || !strings.Contains(text, "[x] Cursor\n\n> Just connect mini") {
		t.Fatalf("screen:\n%s\nwant Windsurf noted, not listed, and the cursor on the first option", text)
	}
	s.handle(press("up"))
	s.handle(press("up"))
	s.handle(press("space"))
	if move, _ := s.handle(press("enter")); move != stay {
		t.Fatalf("enter on an agent row = %v, want it to move to the options", move)
	}
	text = connectText(s)
	leaves := strings.Contains(text, "Leaves Claude, Codex and Cursor as they are")
	if !strings.Contains(text, "[ ] Codex\n") || !leaves {
		t.Errorf("screen:\n%s\nwant Codex unticked, and Don't connect naming every agent it leaves", text)
	}
	s.handle(press("down"))
	if move, _ := s.handle(press("enter")); move != forward || s.chosen != initcmd.DontConnect {
		t.Errorf("enter on Don't connect = %v, chosen %v; want forward with Don't connect", move, s.chosen)
	}
	if got := pickedNames(s); strings.Join(got, ",") != "Claude,Cursor" {
		t.Errorf("picked = %v, want Claude and Cursor", got)
	}
}

func TestConnectScreen_justConnectingAnAgentThatHasMiniSaysItChangesNothing(t *testing.T) {
	list := namedAgents("Claude", "Codex")
	plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"gh"}})
	s, _ := connectScreenFor(t, plan, list, map[string]bool{"Claude": true})
	adds := "Just connect mini\n    Adds mini next to your existing MCPs"
	if text := connectText(s); !strings.Contains(text, adds) {
		t.Fatalf("screen:\n%s\nwant Just connect to add mini while Codex, which lacks it, is ticked", text)
	}
	s.cursor = 1
	s.handle(press("space"))
	changesNothing := "Just connect mini\n    Changes nothing: Claude already has a mini entry."
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
		"> Connect mini and remove existing MCPs\n    checking servers…",
	) {
		t.Fatalf("screen:\n%s\nwant removing first, highlighted, and checking", text)
	}
	if move, _ := s.handle(press("enter")); move != stay || !strings.Contains(s.keys(), "waits for the server checks") {
		t.Fatalf("enter on removing while the checks run = %v, keys %q; want it to wait and say why", move, s.keys())
	}

	plan.checksPass(s, check)
	text := connectText(s)
	count := strings.Contains(text, "Removes 3 MCPs that mini now runs")
	if !count || !strings.Contains(text, "Codex: existing MCPs will be disabled, not removed") {
		t.Errorf("screen:\n%s\nwant 3 MCPs counted across both agents, and the Codex note", text)
	}
	s.handle(press("up"))
	s.handle(press("space"))
	text = connectText(s)
	if !strings.Contains(text, "Removes 2 MCPs") || strings.Contains(text, "Codex: existing") {
		t.Errorf("screen with Codex unticked:\n%s\nwant only Claude's 2 MCPs and no Codex note", text)
	}
	s.handle(press("down"))
	if move, _ := s.handle(press("enter")); move != forward || s.chosen != initcmd.ConnectAndRemove {
		t.Fatalf("enter on removing = %v, chosen %v; want forward with removing", move, s.chosen)
	}
	if got := s.chosenConnect().Removals.ByAgent["Claude"]; strings.Join(got, ",") != "files,notes" {
		t.Errorf("removals passed on = %v, want Claude's files and notes", got)
	}
}

func TestConnectScreen_leavingCancelsTheRunningChecks(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}})
	s, _ := connectScreenFor(t, plan, namedAgents("Claude"), nil)
	if move, _ := s.handle(press("esc")); move != back {
		t.Fatalf("esc = %v, want back", move)
	}
	<-plan.cancelled
}

func TestConnectScreen_leavingDoesNotWaitForTheChecksToStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stuck := &stuckPlan{unblock: make(chan struct{})}
		s := newConnectScreen(connectParams{
			agents: namedAgents("Claude"),
			plan:   func() (connectPlan, error) { return stuck, nil },
		})
		showScreen(s)
		left := make(chan step, 1)
		go func() {
			move, _ := s.handle(press("esc"))
			left <- move
		}()
		synctest.Wait()
		select {
		case move := <-left:
			if move != back {
				t.Errorf("esc = %v, want back", move)
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
	s.handle(press("up"))
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
	s.handle(press("down"))
	lines := strings.Split(ansi.Strip(s.body(7)), "\n")
	if len(lines) > 7 || lines[len(lines)-2] != "> Don't connect" {
		t.Errorf("body at height 7:\n%s\nwant at most 7 lines ending with Don't connect and its subtitle",
			strings.Join(lines, "\n"))
	}
}

func TestConnectScreen_aShortWindowKeepsTheNoteInView(t *testing.T) {
	screenWith := func(t *testing.T, plan *fakePlan) *connectScreen {
		s := newConnectScreen(connectParams{
			agents:   namedAgents("Claude", "Codex"),
			withMini: map[string]bool{"Codex": true},
			plan:     func() (connectPlan, error) { return plan, nil },
		})
		t.Cleanup(s.checks.cancelAndWait)
		s.resize(60, 30)
		plan.checksPass(s, showScreen(s))
		return s
	}

	t.Run("in view while it fits", func(t *testing.T) {
		s := screenWith(t, newFakePlan(nil))
		if text := ansi.Strip(s.body(8)); !strings.HasPrefix(text, "Codex already has a mini entry.") ||
			!strings.Contains(text, "> Just connect mini") {
			t.Errorf("body at height 8:\n%s\nwant the note above the cursor's option", text)
		}
	})

	t.Run("kept above the scrolled rows when the rows don't fit", func(t *testing.T) {
		s := screenWith(t, newFakePlan(map[string][]string{"Claude": {"files"}}))
		for _, key := range []string{"", "down", "down", "up"} {
			if key != "" {
				s.handle(press(key))
			}
			text := ansi.Strip(s.body(5))
			if !strings.HasPrefix(text, "Codex already has a mini entry.\n\n") || !strings.Contains(text, "> ") ||
				strings.Count(text, "\n") > 4 {
				t.Errorf("body at height 5 after %q:\n%s\nwant the note and the cursor row", key, text)
			}
		}
	})
}
