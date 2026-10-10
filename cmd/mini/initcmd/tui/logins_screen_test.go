package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	return framedText(framed(s, false))
}

func TestLoginsScreen_listsLoginsFirstThenWhatMustBeSetUpByHand(t *testing.T) {
	checks := newFakeChecks(
		initcmd.ServerStatus{Name: "ready"},
		initcmd.ServerStatus{Name: "github", Readiness: initcmd.NeedsToken},
		initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLogin},
		initcmd.ServerStatus{Name: "asana", Readiness: initcmd.NeedsOwnApp},
		initcmd.ServerStatus{
			Name: "files", Readiness: initcmd.NeedsEnv, UnsetEnv: &config.UnsetEnvError{Names: []string{"ROOT"}},
		},
	)
	s := checks.screen()
	showScreen(s)
	want := "Log in\n> linear  needs a login\n\nSet up by hand\n  github  needs a token\n" +
		"  asana   needs your own OAuth app\n  files   needs ROOT set\n\n  Continue"
	if text := loginsText(s); text != want {
		t.Errorf("screen:\n%s\nwant:\n%s", text, want)
	}
}

func TestLoginsScreen_isEmptyWhenEveryServerWorks(t *testing.T) {
	s := newFakeChecks(initcmd.ServerStatus{Name: "ready"}).screen()
	s.refresh()
	if !s.empty() {
		t.Errorf("screen:\n%s\nwant it empty, so it is skipped", loginsText(s))
	}
}

func TestLoginsScreen_aServerBeingCheckedSaysSoUntilTheCheckFinishes(t *testing.T) {
	checks := newFakeChecks(initcmd.ServerStatus{Name: "open"})
	checks.checking["open"] = true
	s := checks.screen()
	wait := showScreen(s)
	if text := loginsText(s); !strings.Contains(text, "  open  checking…") || wait == nil {
		t.Fatalf("screen:\n%s\nwait = %v; want open checking and a wait for the result", text, wait)
	}
	if again := showScreen(s); again != nil {
		t.Error("entering again started a second wait; one is enough")
	}

	delete(checks.checking, "open")
	checks.statuses[0].Readiness = initcmd.NeedsLogin
	checks.changed <- struct{}{}
	if next := s.update(wait()); next != nil || !strings.Contains(loginsText(s), "> open  needs a login") {
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
		statuses: func() ([]initcmd.ServerStatus, error) {
			delete(checks.checking, "open")
			checks.changed <- struct{}{}
			return []initcmd.ServerStatus{{Name: "open"}}, nil
		},
		checking: func() map[string]bool { return maps.Clone(checks.checking) },
		changed:  checks.changed,
	})
	wait := showScreen(s)
	if text := loginsText(s); !strings.Contains(text, "open  checking…") || wait == nil {
		t.Errorf(
			"screen:\n%s\nwait = %v; want open still listed as checking, and a wait for the change it signaled",
			text,
			wait,
		)
	}
}

func TestLoginsScreen_saysEverythingIsReadyWhenTheLastCheckFindsNothingToFinish(t *testing.T) {
	checks := newFakeChecks(initcmd.ServerStatus{Name: "open"})
	checks.checking["open"] = true
	s := checks.screen()
	wait := showScreen(s)
	delete(checks.checking, "open")
	checks.changed <- struct{}{}
	s.update(wait())
	if s.heading() != "Your servers are ready" ||
		loginsText(s) != "Every server works; nothing is left to set up.\n\n> Continue" {
		t.Errorf("%s\n%s\nwant the screen to say every server is ready, not an empty list", s.heading(), loginsText(s))
	}
	if keys := framed(s, false).keys(s); keys != "enter continue" {
		t.Errorf("keys = %q, want only enter: no server is left for ↑ to reach", keys)
	}
}

// fakeLogins starts a login per name; each one waits until the test ends it or the screen cancels it.
type fakeLogins struct {
	ends      map[string]chan error
	cancelled chan string
}

func newFakeLogins(names ...string) *fakeLogins {
	f := &fakeLogins{ends: map[string]chan error{}, cancelled: make(chan string, len(names))}
	for _, name := range names {
		f.ends[name] = make(chan error, 1)
	}
	return f
}

func (f *fakeLogins) start(ctx context.Context, name string) (Login, error) {
	wait := func() error {
		select {
		case err := <-f.ends[name]:
			return err
		case <-ctx.Done():
			f.cancelled <- name
			return ctx.Err()
		}
	}
	return Login{URL: "https://auth.example/" + name, Wait: wait}, nil
}

func loginScreen(logins *fakeLogins, names ...string) *loginsScreen {
	var statuses []initcmd.ServerStatus
	for _, name := range names {
		statuses = append(statuses, initcmd.ServerStatus{Name: name, Readiness: initcmd.NeedsLogin})
	}
	checks := newFakeChecks(statuses...)
	s := newLoginsScreen(loginsParams{
		statuses:   func() ([]initcmd.ServerStatus, error) { return checks.statuses, nil },
		checking:   func() map[string]bool { return nil },
		changed:    checks.changed,
		startLogin: logins.start,
	})
	s.resize(80, 30)
	showScreen(s)
	return s
}

// pressAndRun sends key and delivers the screen's commands until it has nothing left to do.
func pressAndRun(s *loginsScreen, key string) {
	_, cmd := s.handle(press(key))
	for cmd != nil {
		cmd = s.update(cmd())
	}
}

func TestLoginsScreen_aLoginShowsItsURLThenHowItEnded(t *testing.T) {
	logins := newFakeLogins("linear", "sentry")
	s := loginScreen(logins, "linear", "sentry")

	_, cmd := s.handle(press("enter"))
	s.update(cmd())
	want := "> linear  waiting for the browser; if it didn't open, use this link:\n" +
		"          https://auth.example/linear\n"
	if text := loginsText(s); !strings.Contains(text, want) {
		t.Fatalf("screen:\n%s\nwant linear waiting with its URL below", text)
	}
	if _, cmd := s.handle(press("enter")); cmd != nil {
		t.Error("enter on linear while its login is pending started it again")
	}
	if _, other := s.handle(press("down")); other != nil {
		t.Fatal("moving while a login is pending started something")
	}
	if _, second := s.handle(press("enter")); second != nil {
		t.Error("enter on sentry while linear's login is pending started a second login")
	}

	logins.ends["linear"] <- nil
	s.update(s.pending.next())
	if text := loginsText(s); !strings.Contains(text, "  linear  ✓ logged in\n> sentry  needs a login") {
		t.Errorf("screen:\n%s\nwant linear done and the cursor on sentry, the next to log in", text)
	}

	logins.ends["sentry"] <- errors.New("access denied")
	pressAndRun(s, "enter")
	if text := loginsText(s); !strings.Contains(text, "  sentry  ✗ access denied\n\n> Continue") {
		t.Errorf("screen:\n%s\nwant sentry's failure shown and the cursor on Continue", text)
	}
}

func TestLoginsScreen_leavingCancelsThePendingLoginAndDropsItsResult(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	_, cmd := s.handle(press("enter"))
	s.update(cmd())

	if a := framed(s, true); send(a, "esc") != nil || a.at != 0 {
		t.Fatalf("at = %d after esc, want back on the screen before", a.at)
	}
	select {
	case <-logins.cancelled:
	default:
		t.Fatal("esc returned before the pending login was cancelled")
	}
	s.update(loginFinished{id: 1, err: context.Canceled})
	if text := loginsText(s); !strings.Contains(text, "> linear  needs a login") {
		t.Errorf("screen:\n%s\nwant the cancelled login's result dropped", text)
	}
}

func TestLoginsScreen_aLongLoginURLWrapsWithinTheWindowAndLinksToTheWholeURL(t *testing.T) {
	url := "https://auth.example/authorize?" + strings.Repeat("scope=read&", 20)
	s := loginScreen(newFakeLogins(), "linear")
	s.p.startLogin = func(ctx context.Context, _ string) (Login, error) {
		return Login{URL: url, Wait: func() error { <-ctx.Done(); return ctx.Err() }}, nil
	}
	t.Cleanup(s.cancelLogin)
	s.resize(40, 30)
	_, cmd := s.handle(press("enter"))
	s.update(cmd())

	body := s.body(40, true)
	var wrapped []string
	for _, line := range strings.Split(ansi.Strip(body), "\n")[1:] {
		if strings.HasPrefix(line, "          ") {
			wrapped = append(wrapped, strings.TrimSpace(line))
		}
		if width := ansi.StringWidth(line); width > 40 && !strings.HasPrefix(line, "> linear") {
			t.Errorf("line %q is %d wide, past the 40-column window", line, width)
		}
	}
	if got := strings.Join(wrapped, ""); got != url || len(wrapped) < 2 {
		t.Errorf("wrapped URL lines = %q; want the whole URL split over several lines", wrapped)
	}
	if links := strings.Count(body, ansi.SetHyperlink(url)); links != len(wrapped) {
		t.Errorf("%d of %d URL lines link to the whole URL", links, len(wrapped))
	}
}

func TestLoginsScreen_aFailedLoginShowsOnlyTheFirstLineOfItsError(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	logins.ends["linear"] <- errors.New("oauth2: cannot fetch token: 502\rBad Gateway\nResponse: <html>\n<body>\x1b[31mdown</body>")
	pressAndRun(s, "enter")
	if text := loginsText(s); text != "Log in\n  linear  ✗ oauth2: cannot fetch token: 502Bad Gateway\n\n> Continue" {
		t.Errorf("screen:\n%q\nwant the error's first line, without control characters, on linear's row", text)
	}
}

func TestLoginsScreen_aTimedOutLoginSaysSoAndCanBeTriedAgain(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	logins.ends["linear"] <- fmt.Errorf("auth flow: %w", context.DeadlineExceeded)
	pressAndRun(s, "enter")
	a := framed(s, false)
	send(a, "up")
	if text := framedText(a); !strings.Contains(text, "> linear  ✗ timed out; enter to try again") {
		t.Errorf("screen:\n%s\nwant the timeout said plainly, with the cursor able to go back to linear", text)
	}
	if retry := send(a, "enter"); retry == nil {
		t.Error("enter on the timed-out row started no login")
	}
	t.Cleanup(s.cancelLogin)
}

func TestLoginsScreen_theCursorStaysOnTheUsersPickWhenACheckFinishes(t *testing.T) {
	setup := func() (*fakeChecks, *loginsScreen, tea.Cmd) {
		checks := newFakeChecks(
			initcmd.ServerStatus{Name: "open"},
			initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLogin},
			initcmd.ServerStatus{Name: "sentry", Readiness: initcmd.NeedsLogin},
		)
		checks.checking["open"] = true
		s := checks.screen()
		return checks, s, showScreen(s)
	}
	finish := func(checks *fakeChecks, s *loginsScreen, wait tea.Cmd, readiness initcmd.Readiness) {
		delete(checks.checking, "open")
		checks.statuses[0].Readiness = readiness
		checks.changed <- struct{}{}
		s.update(wait())
	}
	t.Run("a row above it drops out", func(t *testing.T) {
		checks, s, wait := setup()
		s.handle(press("down"))
		finish(checks, s, wait, initcmd.Ready)
		if text := loginsText(s); !strings.Contains(text, "> sentry") {
			t.Errorf("screen:\n%s\nwant the cursor still on sentry", text)
		}
	})
	t.Run("on Continue, a new login row doesn't pull it back", func(t *testing.T) {
		checks, s, wait := setup()
		a := framed(s, false)
		send(a, "down", "down")
		finish(checks, s, wait, initcmd.NeedsLogin)
		if text := framedText(a); !strings.Contains(text, "> Continue") {
			t.Errorf("screen:\n%s\nwant the cursor still on Continue", text)
		}
	})
	t.Run("unmoved, it rests on the first login to do", func(t *testing.T) {
		checks, s, wait := setup()
		finish(checks, s, wait, initcmd.NeedsLogin)
		if text := loginsText(s); !strings.Contains(text, "> open") {
			t.Errorf("screen:\n%s\nwant the cursor on open, now the first login", text)
		}
	})
}

func TestLoginsScreen_aShortWindowKeepsThePendingLoginsURLInView(t *testing.T) {
	logins := newFakeLogins("linear", "sentry", "notion")
	s := loginScreen(logins, "linear", "sentry", "notion")
	s.resize(30, 30)
	s.handle(press("down"))
	_, cmd := s.handle(press("enter"))
	s.update(cmd())
	t.Cleanup(s.cancelLogin)
	all := strings.Split(ansi.Strip(s.body(0, true)), "\n")
	if len(all) < 5 || !strings.HasPrefix(all[2], "> sentry") {
		t.Fatalf(
			"full body:\n%s\nwant sentry's row after the heading, its URL wrapped over two lines",
			strings.Join(all, "\n"),
		)
	}
	sentryAndURL := strings.Join(all[2:5], "\n")

	if body := ansi.Strip(s.body(3, true)); body != sentryAndURL {
		t.Errorf("body at height 3:\n%s\nwant sentry's row and its whole URL:\n%s", body, sentryAndURL)
	}
	if body := ansi.Strip(s.body(2, true)); body != strings.Join(all[2:4], "\n") {
		t.Errorf("body at height 2:\n%s\nwant sentry's row kept when its URL doesn't fit", body)
	}
}

func TestLoginsScreen_movingUpMovesTheCursorBeforeTheView(t *testing.T) {
	names := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"}
	s := loginScreen(newFakeLogins(names...), names...)
	s.resize(60, 30)
	for range names[1:] {
		s.handle(press("down"))
		s.body(4, true)
	}
	before := strings.Split(ansi.Strip(s.body(4, true)), "\n")
	s.handle(press("up"))
	after := strings.Split(ansi.Strip(s.body(4, true)), "\n")
	if !slices.Equal(stripMarks(before), stripMarks(after)) || !strings.HasPrefix(after[2], "> s7") {
		t.Errorf("view before up:\n%s\nafter:\n%s\nwant the same rows, the cursor one up on s7",
			strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
}

func stripMarks(lines []string) []string {
	var stripped []string
	for _, line := range lines {
		stripped = append(stripped, strings.TrimPrefix(strings.TrimPrefix(line, "> "), "  "))
	}
	return stripped
}

func TestLoginsScreen_showingItAgainPutsTheCursorOnTheNextLogin(t *testing.T) {
	s := loginScreen(newFakeLogins("linear", "sentry"), "linear", "sentry")
	s.handle(press("down"))
	s.handle(press("down"))

	showScreen(s)

	if got := s.cursorName(); got != "linear" {
		t.Errorf("cursor on %q after showing Logins again, want linear: it rests on the next login to do", got)
	}
	if s.refresh(); s.cursorName() != "linear" {
		t.Errorf("cursor moved to %q when the rows were read again, want it to stay on linear", s.cursorName())
	}
}

func TestLoginsScreen_aReadErrorLeavesOnlyTheNavigation(t *testing.T) {
	s := newLoginsScreen(loginsParams{
		statuses: func() ([]initcmd.ServerStatus, error) { return nil, errors.New("permission denied") },
		checking: func() map[string]bool { return nil },
	})
	showScreen(s)
	a := framed(s, true)
	send(a, "down")
	if text := framedText(a); !strings.HasSuffix(text, "permission denied\n\n  Continue\n> Back") {
		t.Errorf("screen:\n%s\nwant the error above Continue and Back, with the cursor on Back", text)
	}
}

func TestLoginsScreen_tabJumpsToContinueAndUpReturnsToTheServerItLeft(t *testing.T) {
	s := newFakeChecks(
		initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLogin},
		initcmd.ServerStatus{Name: "sentry", Readiness: initcmd.NeedsLogin},
	).screen()
	showScreen(s)
	a := framed(s, false)
	send(a, "tab")
	if text := framedText(a); !strings.HasSuffix(text, "> Continue") || !strings.HasPrefix(a.keys(s), "↑ move") {
		t.Fatalf("screen after tab:\n%s\nkeys %q; want the cursor on Continue and ↑ named", text, a.keys(s))
	}
	send(a, "up")
	if text := framedText(a); !strings.Contains(text, "> linear") {
		t.Errorf("screen after up:\n%s\nwant the cursor back on linear", text)
	}
	send(a, "down", "down", "up")
	if text := framedText(a); !strings.Contains(text, "> sentry") {
		t.Errorf("screen after down past sentry and up:\n%s\nwant the cursor back on sentry", text)
	}
}

func TestLoginsScreen_upFromContinueFindsTheServerItLeftAfterARowAboveGoes(t *testing.T) {
	f := newFakeChecks(
		initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLogin},
		initcmd.ServerStatus{Name: "sentry", Readiness: initcmd.NeedsLogin},
		initcmd.ServerStatus{Name: "notion", Readiness: initcmd.NeedsLogin},
	)
	s := f.screen()
	showScreen(s)
	a := framed(s, false)
	send(a, "down", "tab")
	f.statuses = f.statuses[1:]
	s.refresh()
	send(a, "up")
	if text := framedText(a); !strings.Contains(text, "> sentry") {
		t.Errorf("screen after linear went and up from Continue:\n%s\nwant the cursor back on sentry", text)
	}
}

func TestLoginsScreen_onContinueTheLastLoginsResultStaysInView(t *testing.T) {
	var names []string
	for i := range 12 {
		names = append(names, fmt.Sprintf("srv%02d", i))
	}
	logins := newFakeLogins(names...)
	s := loginScreen(logins, names...)
	for i, name := range names {
		var err error
		if i == len(names)-1 {
			err = errors.New("access denied")
		}
		logins.ends[name] <- err
		pressAndRun(s, "enter")
	}
	a := framed(s, false)
	if body := ansi.Strip(
		a.body(8),
	); !strings.Contains(body, "srv11  ✗ access denied") ||
		!strings.Contains(body, "> Continue") {
		t.Errorf("body at height 8:\n%s\nwant the failed srv11 in view above Continue", body)
	}
}
