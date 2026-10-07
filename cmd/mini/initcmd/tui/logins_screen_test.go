package tui

import (
	"maps"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/config"
)

type fakeChecks struct {
	statuses []initcmd.ServerStatus
	checking map[string]bool
	changed  chan struct{}
}

func newFakeChecks(statuses ...initcmd.ServerStatus) *fakeChecks {
	return &fakeChecks{statuses: statuses, checking: map[string]bool{}, changed: make(chan struct{}, 1)}
}

func (f *fakeChecks) screen() *loginsScreen {
	return newLoginsScreen(loginsParams{
		statuses: func() ([]initcmd.ServerStatus, error) { return f.statuses, nil },
		checking: func() map[string]bool { return maps.Clone(f.checking) },
		changed:  f.changed,
	})
}

func loginsText(s *loginsScreen) string {
	return ansi.Strip(s.body(20))
}

func TestLoginsScreen_listsWhatEachServerStillNeeds(t *testing.T) {
	checks := newFakeChecks(
		initcmd.ServerStatus{Name: "ready"},
		initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLogin},
		initcmd.ServerStatus{Name: "github", Readiness: initcmd.NeedsToken},
		initcmd.ServerStatus{Name: "asana", Readiness: initcmd.NeedsOwnApp},
		initcmd.ServerStatus{
			Name: "files", Readiness: initcmd.NeedsEnv, UnsetEnv: &config.UnsetEnvError{Names: []string{"ROOT"}},
		},
	)
	s := checks.screen()
	s.enter()
	want := "  linear  needs a login: mini auth linear\n  github  needs a token\n" +
		"  asana   needs your own OAuth app\n  files   needs ROOT set"
	if text := loginsText(s); text != want {
		t.Errorf("screen:\n%s\nwant:\n%s", text, want)
	}
}

func TestLoginsScreen_isEmptyWhenEveryServerWorks(t *testing.T) {
	if s := newFakeChecks(initcmd.ServerStatus{Name: "ready"}).screen(); !s.empty() {
		t.Errorf("screen:\n%s\nwant it empty, so it is skipped", loginsText(s))
	}
}

func TestLoginsScreen_aServerBeingCheckedSaysSoUntilTheCheckFinishes(t *testing.T) {
	checks := newFakeChecks(initcmd.ServerStatus{Name: "open"})
	checks.checking["open"] = true
	s := checks.screen()
	wait := s.enter()
	if text := loginsText(s); !strings.Contains(text, "open  checking…") || wait == nil {
		t.Fatalf("screen:\n%s\nwait = %v; want open checking and a wait for the result", text, wait)
	}
	if again := s.enter(); again != nil {
		t.Error("entering again started a second wait; one is enough")
	}

	delete(checks.checking, "open")
	checks.statuses[0].Readiness = initcmd.NeedsLogin
	checks.changed <- struct{}{}
	if next := s.update(wait()); next != nil || !strings.Contains(loginsText(s), "open  needs a login") {
		t.Errorf(
			"after the check: screen:\n%s\nnext wait = %v; want the login need and no more waiting",
			loginsText(s),
			next,
		)
	}
}

func TestLoginsScreen_aCheckFinishingWhileTheStatusesAreReadIsNotLost(t *testing.T) {
	checks := newFakeChecks()
	checks.checking["open"] = true
	s := newLoginsScreen(loginsParams{
		// The statuses were read just before the check recorded its result; it ends right after.
		statuses: func() ([]initcmd.ServerStatus, error) {
			delete(checks.checking, "open")
			checks.changed <- struct{}{}
			return []initcmd.ServerStatus{{Name: "open"}}, nil
		},
		checking: func() map[string]bool { return maps.Clone(checks.checking) },
		changed:  checks.changed,
	})
	wait := s.enter()
	if text := loginsText(s); !strings.Contains(text, "open  checking…") || wait == nil {
		t.Errorf(
			"screen:\n%s\nwait = %v; want open still listed as checking, and a wait for the change it signaled",
			text,
			wait,
		)
	}
}
