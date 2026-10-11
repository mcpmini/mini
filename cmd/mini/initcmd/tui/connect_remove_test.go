package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

func TestConnectScreen_removingWaitsForTheChecksThenCountsTheTickedAgents(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files", "notes"}, "Codex": {"github"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
	checking := "> Connect mini and remove existing MCPs\n  Checking which MCPs mini can run…\n  Just connect mini"
	if text := connectText(s); !strings.Contains(text, checking) {
		t.Fatalf("screen:\n%s\nwant the cursor on removing, which says it is checking", text)
	}
	if s.choose(0) {
		t.Fatal("choose(removing) while the checks run = true, want refused")
	}

	plan.checksPass(s, check)
	want := "    Claude: Removing files, notes\n" +
		"    Codex:  Disabling github"
	if text := connectText(s); !strings.Contains(text, want) {
		t.Errorf("screen:\n%s\nwant each agent's entries named, Codex's disabled:\n%s", text, want)
	}
	a := framed(s, false)
	send(a, "up", "space")
	text := framedText(a)
	if !strings.Contains(text, "Claude: Removing files, notes") || strings.Contains(text, "Disabling") {
		t.Errorf("screen with Codex unticked:\n%s\nwant only Claude's entries", text)
	}
	if cmd := send(a, "down", "enter"); cmd == nil || s.chosen != initcmd.ConnectAndRemove {
		t.Fatalf("enter on removing = %v, chosen %v; want the app to finish with removing", cmd, s.chosen)
	}
	if got := s.chosenConnect().Removals.ByAgent["Claude"]; strings.Join(got, ",") != "files,notes" {
		t.Errorf("removals passed on = %v, want Claude's files and notes", got)
	}
}

func TestConnectScreen_aCheckRunFromAnEarlierVisitIsIgnored(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}})
	s, earlier := connectScreenFor(t, plan, namedAgents("Claude"), nil)
	later := showScreen(s)
	<-plan.cancelled
	s.update(earlier())
	if text := connectText(s); !strings.Contains(text, "Checking which MCPs mini can run…") {
		t.Fatalf("screen:\n%s\nwant the earlier visit's result ignored while this visit's checks run", text)
	}
	plan.checksPass(s, later)
	if text := connectText(s); !strings.Contains(text, "Claude: Removing files") {
		t.Errorf("screen:\n%s\nwant this visit's result shown", text)
	}
}

func TestConnectScreen_removingSaysWhatTheChecksLeft(t *testing.T) {
	t.Run("an agent with nothing to remove gets no line", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
		plan.checksPass(s, check)
		text := connectText(s)
		if strings.Contains(text, "Codex:") || !strings.Contains(text, "Claude: Removing files") {
			t.Errorf("screen:\n%s\nwant Claude's removal only: Codex has nothing mini replaces", text)
		}
	})
	t.Run("nothing passed names the ticked agents' MCPs that don't work yet", func(t *testing.T) {
		plan := newFakePlan(
			map[string][]string{"Claude": {"linear"}, "Codex": {"sentry", "linear"}, "Cursor": {"datadog"}},
		)
		s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex", "Cursor"), nil)
		s.ticked["Cursor"] = false
		plan.release <- initcmd.Removals{}
		s.update(check())
		want := "Nothing will be removed: linear and sentry don't work in mini yet."
		if text := connectText(s); !strings.Contains(text, want) || strings.Contains(text, "backed up") {
			t.Errorf("screen:\n%s\nwant it to name linear and sentry, and not Cursor's datadog", text)
		}
		s.ticked["Codex"] = false
		want = "Nothing will be removed: linear doesn't work in mini yet."
		if text := connectText(s); !strings.Contains(text, want) {
			t.Errorf("screen with only Claude ticked:\n%s\nwant only linear named", text)
		}
	})
	t.Run("a server needing a login says to log in to it", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"linear"}, "Codex": {"sentry", "linear"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
		s.p.statuses = func() ([]initcmd.ServerStatus, error) {
			return []initcmd.ServerStatus{{Name: "linear", Readiness: initcmd.NeedsLoginState{}}, {Name: "sentry"}}, nil
		}
		plan.release <- initcmd.Removals{}
		s.update(check())
		want := "Nothing will be removed: log in to linear; sentry doesn't work in mini yet."
		if text := connectText(s); !strings.Contains(text, want) {
			t.Errorf("screen:\n%s\nwant %q", text, want)
		}
		s.ticked["Codex"] = false
		want = "Nothing will be removed until you log in to linear."
		if text := connectText(s); !strings.Contains(text, want) {
			t.Errorf("screen with only Claude ticked:\n%s\nwant %q", text, want)
		}
	})
	t.Run("an agent that has mini and nothing to remove gets no line", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"github"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), map[string]bool{"Claude": true})
		plan.release <- initcmd.Removals{ByAgent: map[string][]string{"Codex": {"github"}}}
		s.update(check())
		text := connectText(s)
		if strings.Contains(text, "Claude:") || !strings.Contains(text, "Codex: Disabling github") {
			t.Errorf("screen:\n%s\nwant only Codex's line: Claude has nothing to remove", text)
		}
	})
	t.Run("a narrow window wraps the subtitle", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files", "notes", "calendar", "drive"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude"), nil)
		s.resize(44, 30)
		plan.checksPass(s, check)
		lines := strings.Split(linesUnder(s, initcmd.ConnectAndRemove), "\n")
		for _, line := range lines {
			if ansi.StringWidth(line) > 44 {
				t.Errorf("line %q is wider than the 44-column window", line)
			}
		}
		if last := lines[len(lines)-1]; len(lines) < 3 || !strings.HasPrefix(last, "    ") {
			t.Errorf("lines:\n%s\nwant Claude's line wrapped, its rest indented under it", strings.Join(lines, "\n"))
		}
	})
}

func TestConnectScreen_removingStaysListedWhileTicksChangeAndIsDimWithoutRemovals(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Codex": {"github"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
	plan.checksPass(s, check)
	s.handle(press("space"))
	if got := s.choices(); !slices.Contains(got, optionLabel(initcmd.ConnectAndRemove)) {
		t.Fatalf("choices with Codex unticked = %v, want removing still listed: a listed agent has entries", got)
	}
	want := "Nothing to remove from the ticked agents."
	if !s.disabled(0) || !strings.Contains(linesUnder(s, initcmd.ConnectAndRemove), want) {
		t.Errorf("removing with Claude alone ticked: disabled %v, lines %q; want dim with %q",
			s.disabled(0), linesUnder(s, initcmd.ConnectAndRemove), want)
	}
	s.handle(press("space"))
	if s.disabled(0) {
		t.Errorf("removing with Codex ticked again is disabled, want enabled: Codex has entries to remove")
	}
}

func TestConnectScreen_removingFromAnAgentThatHasMiniOnlyRemoves(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"github"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), map[string]bool{"Claude": true})
	plan.checksPass(s, check)
	if text := connectText(s); !strings.Contains(text, "Claude: Removing files\n") {
		t.Errorf("screen:\n%s\nwant Claude's entry removed without adding mini again", text)
	}
}

func TestConnectScreen_anAgentThatAlreadyHasMiniIsNotToldMiniWillBeAdded(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude"), map[string]bool{"Claude": true})
	plan.checksPass(s, check)
	removing, connecting := linesUnder(s, initcmd.ConnectAndRemove), linesUnder(s, initcmd.ConnectOnly)
	if strings.Contains(removing, "Adds mini") || !strings.Contains(removing, "Claude: Removing files") {
		t.Errorf(
			"lines under removing:\n%s\nwant only the backup and Claude's removal: mini is already there",
			removing,
		)
	}
	if want := "mini is already connected, so nothing changes."; !strings.Contains(
		connecting,
		want,
	) {
		t.Errorf("lines under just connecting:\n%s\nwant %q", connecting, want)
	}
}
