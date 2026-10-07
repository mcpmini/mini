package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
)

func namedAgents(names ...string) []agents.Agent {
	var list []agents.Agent
	for _, name := range names {
		list = append(list, agents.Agent{Name: name, ConfigPath: "/home/u/" + name + ".json"})
	}
	return list
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
	s := newConnectScreen(namedAgents("Claude"), nil)
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
	s := newConnectScreen(namedAgents("Claude", "Codex", "Cursor"), map[string]bool{"Cursor": true})
	if text := connectText(s); !strings.Contains(text, "[x] Cursor  already runs mini\n\n> Just connect mini") {
		t.Fatalf("screen:\n%s\nwant Cursor marked as running mini and the cursor on the first option", text)
	}
	s.handle(press("up"))
	s.handle(press("up"))
	s.handle(press("space"))
	if move, _ := s.handle(press("enter")); move != stay {
		t.Fatalf("enter on an agent row = %v, want it to move to the options", move)
	}
	if text := connectText(
		s,
	); !strings.Contains(text, "[ ] Codex ") ||
		!strings.Contains(text, "Leaves Claude and Cursor as they are") {
		t.Errorf("screen:\n%s\nwant Codex unticked and Don't connect naming the agents still ticked", text)
	}
	s.handle(press("down"))
	if move, _ := s.handle(press("enter")); move != forward || s.chosen != initcmd.DontConnect {
		t.Errorf("enter on Don't connect = %v, chosen %v; want forward with Don't connect", move, s.chosen)
	}
	if got := pickedNames(s); strings.Join(got, ",") != "Claude,Cursor" {
		t.Errorf("picked = %v, want Claude and Cursor", got)
	}
}

func TestConnectScreen_isEmptyWhenEveryAgentRunsMini(t *testing.T) {
	list := namedAgents("Claude", "Codex")
	if s := newConnectScreen(list, map[string]bool{"Claude": true, "Codex": true}); !s.empty() {
		t.Error("Connect is shown although every agent already runs mini")
	}
	if s := newConnectScreen(list, map[string]bool{"Claude": true}); s.empty() {
		t.Error("Connect is skipped although Codex doesn't run mini")
	}
	if s := newConnectScreen(nil, nil); !s.empty() {
		t.Error("Connect is shown with no agent to connect")
	}
}
