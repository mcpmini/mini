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
	want := "The configs are backed up first\n" +
		"    Claude: adding mini, removing files, notes\n" +
		"    Codex: adding mini, disabling github"
	if text := connectText(s); !strings.Contains(text, want) {
		t.Errorf("screen:\n%s\nwant each agent's entries named, Codex's disabled:\n%s", text, want)
	}
	send(a, "up", "space")
	if text := framedText(a); !strings.Contains(text, "removing files, notes") || strings.Contains(text, "disabling") {
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
	if text := connectText(s); !strings.Contains(text, "checking servers…") {
		t.Fatalf("screen:\n%s\nwant the earlier visit's result ignored while this visit's checks run", text)
	}
	plan.checksPass(s, later)
	if text := connectText(s); !strings.Contains(text, "Claude: adding mini, removing files") {
		t.Errorf("screen:\n%s\nwant this visit's result shown", text)
	}
}

func TestConnectScreen_removingSaysWhatTheChecksLeft(t *testing.T) {
	t.Run("an agent with nothing mini runs keeps its entries", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
		plan.checksPass(s, check)
		if text := connectText(s); !strings.Contains(text, "Codex: adding mini, nothing to remove") {
			t.Errorf("screen:\n%s\nwant Codex to only gain mini", text)
		}
	})
	t.Run("nothing passed", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude"), nil)
		plan.release <- initcmd.Removals{}
		s.update(check())
		want := "Nothing to remove: none of these MCPs work in mini yet"
		if text := connectText(s); !strings.Contains(text, want) || strings.Contains(text, "backed up") {
			t.Errorf("screen:\n%s\nwant it to say nothing can be removed yet", text)
		}
	})
	t.Run("nothing passed for an agent that has mini", func(t *testing.T) {
		plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"github"}})
		s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), map[string]bool{"Claude": true})
		plan.release <- initcmd.Removals{ByAgent: map[string][]string{"Codex": {"github"}}}
		s.update(check())
		if text := connectText(s); !strings.Contains(text, "Claude: mini already connected, nothing to remove") {
			t.Errorf("screen:\n%s\nwant Claude told nothing changes for it", text)
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

func TestConnectScreen_removingIsOfferedOnlyWhileATickedAgentHasEntriesMiniRuns(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Codex": {"github"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), nil)
	plan.checksPass(s, check)
	s.handle(press("space"))
	if got := s.choices(); slices.Contains(got, optionLabel(initcmd.ConnectAndRemove)) {
		t.Errorf("choices with Codex unticked = %v, want no removing: Claude has nothing mini runs", got)
	}
	s.handle(press("space"))
	if got := s.choices(); !slices.Contains(got, optionLabel(initcmd.ConnectAndRemove)) {
		t.Errorf("choices with Codex ticked again = %v, want removing back", got)
	}
}

func TestConnectScreen_removingFromAnAgentThatHasMiniOnlyRemoves(t *testing.T) {
	plan := newFakePlan(map[string][]string{"Claude": {"files"}, "Codex": {"github"}})
	s, check := connectScreenFor(t, plan, namedAgents("Claude", "Codex"), map[string]bool{"Claude": true})
	plan.checksPass(s, check)
	if text := connectText(s); !strings.Contains(text, "Claude: removing files\n") {
		t.Errorf("screen:\n%s\nwant Claude's entry removed without adding mini again", text)
	}
}
