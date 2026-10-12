//go:build test

package tui

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/clock"
)

func TestLoginsScreen_theSpinnerTurnsUntilTheChecksFinish(t *testing.T) {
	checks := newFakeChecks(initcmd.ServerStatus{Name: "open"})
	checks.checking["open"] = true
	s := checks.screen()
	fake := clock.NewFake()
	s.p.clock = fake
	wait := showScreen(s)
	fake.Advance(spinnerInterval)
	wait = s.update(wait())
	if text := loginsText(s); !strings.HasPrefix(text, "⠙ Checking") || wait == nil {
		t.Fatalf(
			"screen after a frame:\n%s\nwait = %v; want the spinner's next frame and a wait for the next",
			text,
			wait,
		)
	}
	delete(checks.checking, "open")
	checks.statuses[0].Readiness = initcmd.NeedsLoginState{}
	checks.changed <- struct{}{}
	if next := s.update(wait()); next != nil || !strings.Contains(loginsText(s), "> open  press enter to log in") {
		t.Errorf("screen after the check:\n%s\nnext = %v; want the row and the spinner stopped", loginsText(s), next)
	}
}
