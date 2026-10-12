package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
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
		configDirFor: func(string) string { return "testconfig" },
		steps:        testSteps,
		statuses:     func() ([]initcmd.ServerStatus, error) { return f.statuses, nil },
		checking:     func() map[string]bool { return maps.Clone(f.checking) },
		changed:      f.changed,
	})
}

var testSteps = serverSteps{
	file:  func(name string) string { return "~/.mini/servers/" + name + ".yaml" },
	login: func(name string) string { return "mini auth " + name },
}

// The warnings unwrapped, as a 200-column window shows them.
const (
	oneLoginLeft = "\n\n⚠ mini can't use this MCP until you log in to it. " +
		"You can always log in later using mini auth <name>."
	loginsLeft = "\n\n⚠ mini can't use these MCPs until you log in to them. " +
		"You can always log in later using mini auth <name>."
)

func loginsText(s *loginsScreen) string {
	return framedText(framed(s, false))
}

func TestLoginsScreen_listsOnlyLoginRowsAndListsTheByHandServersWithWhatTheyRequire(t *testing.T) {
	checks := newFakeChecks(
		initcmd.ServerStatus{Name: "ready"},
		initcmd.ServerStatus{Name: "github", Readiness: initcmd.NeedsTokenState{}},
		initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLoginState{}},
		initcmd.ServerStatus{Name: "asana", Readiness: initcmd.NeedsOwnAppState{}},
		initcmd.ServerStatus{Name: "files", Readiness: initcmd.NeedsEnvState{}},
	)
	s := checks.screen()
	s.resize(120, 30)
	showScreen(s)
	want := "> linear  press enter to log in" + oneLoginLeft + "\n\n" +
		"Browser login isn't available for these MCPs, so you will need to configure them manually later:\n" +
		"  github  requires manual configuration, edit ~/.mini/servers/github.yaml\n" +
		"  asana   requires a custom OAuth app, edit ~/.mini/servers/asana.yaml\n" +
		"  files   requires manual configuration, edit ~/.mini/servers/files.yaml\n\n  Continue"
	if text := loginsText(s); text != want {
		t.Errorf("screen:\n%s\nwant:\n%s", text, want)
	}
	if heading := s.heading(); heading != "Log in to your MCPs (optional)" {
		t.Errorf("heading = %q, want it to offer the optional login", heading)
	}
}

func TestLoginsScreen_theCursorsUntriedRowAloneGetsAHint(t *testing.T) {
	s := loginScreen(newFakeLogins(), "linear", "sentry")
	s.resize(200, 30)
	want := "> linear  press enter to log in\n  sentry" + loginsLeft + "\n\n  Continue"
	if text := loginsText(s); text != want {
		t.Errorf("screen:\n%s\nwant:\n%s", text, want)
	}
}

func TestLoginsScreen_withTheCursorOnContinueNoRowGetsAHint(t *testing.T) {
	s := loginScreen(newFakeLogins(), "linear", "sentry")
	a := framed(s, false)
	send(a, "tab")
	if text := framedText(a); strings.Contains(text, "press enter") || !strings.HasSuffix(text, "> Continue") {
		t.Errorf("screen:\n%s\nwant Continue focused and no hint under any row", text)
	}
}

func TestLoginsScreen_byHandServersAreListedBelowTheLoginRowsAndAreNotRows(t *testing.T) {
	cases := []struct {
		name   string
		byHand []string
		list   string
	}{
		{
			"one",
			[]string{"github"},
			"Browser login isn't available for this MCP, so you will need to configure it manually later:\n" +
				"  github  requires manual configuration, edit ~/.mini/servers/github.yaml",
		},
		{
			"two",
			[]string{"github", "asana"},
			"Browser login isn't available for these MCPs, so you will need to configure them manually later:\n" +
				"  github  requires manual configuration, edit ~/.mini/servers/github.yaml\n" +
				"  asana   requires manual configuration, edit ~/.mini/servers/asana.yaml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statuses := []initcmd.ServerStatus{{Name: "linear", Readiness: initcmd.NeedsLoginState{}}}
			for _, name := range tc.byHand {
				statuses = append(statuses, initcmd.ServerStatus{Name: name, Readiness: initcmd.NeedsTokenState{}})
			}
			s := loginScreenWithStatuses(newFakeLogins(), statuses...)
			s.resize(200, 30)
			a := framed(s, false)
			rows := "> linear  press enter to log in" + oneLoginLeft
			if text := framedText(a); !strings.HasPrefix(text, rows+"\n\n"+tc.list+"\n\n") {
				t.Fatalf("screen:\n%s\nwant the list below the login rows", text)
			}
			send(a, "down")
			if text := framedText(a); text != "  linear"+oneLoginLeft+"\n\n"+tc.list+"\n\n> Continue" {
				t.Errorf(
					"screen after down:\n%s\nwant the by-hand servers out of the cursor's reach, Continue next",
					text,
				)
			}
		})
	}
}

func TestLoginsScreen_onlyByHandServersLeaveAHeadingAndTheListWithContinue(t *testing.T) {
	s := loginScreenWithStatuses(
		newFakeLogins(),
		initcmd.ServerStatus{Name: "github", Readiness: initcmd.NeedsTokenState{}},
	)
	s.resize(200, 30)
	a := framed(s, false)
	want := "Browser login isn't available for this MCP, so you will need to configure it manually later:\n" +
		"  github  requires manual configuration, edit ~/.mini/servers/github.yaml\n\n> Continue"
	if text := framedText(a); text != want {
		t.Errorf("screen:\n%s\nwant:\n%s", text, want)
	}
	if heading := s.heading(); heading != "Finish setting up these MCPs later" {
		t.Errorf("heading = %q, want it to say the by-hand servers are for later", heading)
	}
}

func TestLoginsScreen_defaultFooterNamesOpeningTheBrowser(t *testing.T) {
	s := loginScreen(newFakeLogins(), "linear")
	if keys := s.keys(); keys != "↑↓ move · enter open browser · tab continue" {
		t.Errorf("keys = %q, want enter named as opening the browser", keys)
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
	if text := loginsText(s); strings.Contains(text, "⚠") {
		t.Errorf("screen:\n%s\nwant no login warning while the check may still find open ready", text)
	}
	if again := showScreen(s); again != nil {
		t.Error("entering again started a second wait; one is enough")
	}

	delete(checks.checking, "open")
	checks.statuses[0].Readiness = initcmd.NeedsLoginState{}
	checks.changed <- struct{}{}
	if next := s.update(wait()); next != nil || !strings.Contains(loginsText(s), "> open  press enter to log in") {
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
		configDirFor: func(string) string { return "testconfig" },
		steps:        testSteps,
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
	if s.heading() != "Your MCPs are ready" ||
		loginsText(s) != "Every MCP works; nothing is left to set up.\n\n> Continue" {
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

func (f *fakeLogins) start(ctx context.Context, _, name string) (Login, error) {
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
		statuses = append(statuses, initcmd.ServerStatus{Name: name, Readiness: initcmd.NeedsLoginState{}})
	}
	return loginScreenWithStatuses(logins, statuses...)
}

func loginScreenWithStatuses(logins *fakeLogins, statuses ...initcmd.ServerStatus) *loginsScreen {
	checks := newFakeChecks(statuses...)
	s := newLoginsScreen(loginsParams{
		configDirFor: func(string) string { return "testconfig" },
		steps:        testSteps,
		statuses:     func() ([]initcmd.ServerStatus, error) { return checks.statuses, nil },
		checking:     func() map[string]bool { return nil },
		changed:      checks.changed,
		startLogin:   logins.start,
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
	if text := loginsText(s); !strings.Contains(text, "  linear  ✓ logged in\n> sentry  press enter to log in") {
		t.Errorf("screen:\n%s\nwant linear done with no hint, and the cursor on sentry, the next to log in", text)
	}

	logins.ends["sentry"] <- errors.New("access denied")
	pressAndRun(s, "enter")
	failed := "  sentry  ✗ login failed: access denied\n\n⚠ mini can't use this MCP"
	if text := loginsText(s); !strings.Contains(text, failed) {
		t.Errorf("screen:\n%s\nwant sentry's failure shown, then the warning that it still needs a login", text)
	}
}

func TestLoginsScreen_escCancelsTheWaitingLoginAndStays(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	_, cmd := s.handle(press("enter"))
	s.update(cmd())

	a := framed(s, true)
	if send(a, "esc"); a.at != 1 {
		t.Fatalf("at = %d after esc on a waiting login, want Logins kept", a.at)
	}
	select {
	case <-logins.cancelled:
	default:
		t.Fatal("esc returned before the waiting login was cancelled")
	}
	s.update(loginFinished{id: 1, err: context.Canceled})
	if text := framedText(a); !strings.Contains(text, "> linear  press enter to log in") {
		t.Errorf("screen:\n%s\nwant the cancelled login's result dropped", text)
	}
	if send(a, "esc"); a.at != 0 {
		t.Errorf("at = %d after esc with no login waiting, want back on the screen before", a.at)
	}
}

func TestLoginsScreen_leavingCancelsTheWaitingLogin(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	_, cmd := s.handle(press("enter"))
	s.update(cmd())
	a := framed(s, true)
	if send(a, "tab", "down", "enter"); a.at != 0 {
		t.Fatalf("at = %d after enter on Back, want the screen before", a.at)
	}
	select {
	case <-logins.cancelled:
	default:
		t.Error("Back left Logins with its login still waiting")
	}
}

func TestLoginsScreen_aLongLoginURLWrapsWithinTheWindowAndLinksToTheWholeURL(t *testing.T) {
	url := "https://auth.example/authorize?" + strings.Repeat("scope=read&", 20)
	s := loginScreen(newFakeLogins(), "linear")
	s.p.startLogin = func(ctx context.Context, _, _ string) (Login, error) {
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

func TestLoginsScreen_theWarningGoesOnceEveryMCPIsLoggedIn(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	s.resize(200, 30)
	if text := loginsText(s); !strings.Contains(text, oneLoginLeft) {
		t.Fatalf("screen:\n%s\nwant the warning while linear needs a login", text)
	}
	logins.ends["linear"] <- nil
	pressAndRun(s, "enter")
	if text := loginsText(s); text != "  linear  ✓ logged in\n\n> Continue" {
		t.Errorf("screen:\n%s\nwant no warning: nothing is left to log in to", text)
	}
}

func TestLoginsScreen_aFailedLoginWarnsNoMoreOnceACheckFindsTheMCPReady(t *testing.T) {
	logins := newFakeLogins("linear")
	checks := newFakeChecks(initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLoginState{}})
	s := checks.screen()
	s.p.startLogin = logins.start
	s.resize(200, 30)
	showScreen(s)
	logins.ends["linear"] <- errors.New("access denied")
	pressAndRun(s, "enter")
	if text := loginsText(s); !strings.Contains(text, oneLoginLeft) {
		t.Fatalf("screen:\n%s\nwant the warning after the failed login", text)
	}
	checks.statuses[0].Readiness = initcmd.ReadyState{}
	s.refresh()
	if text := loginsText(s); strings.Contains(text, "⚠") {
		t.Errorf("screen:\n%s\nwant no warning: linear works now", text)
	}
}

func TestLoginsScreen_aFailedLoginShowsOnlyTheFirstLineOfItsError(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	logins.ends["linear"] <- errors.New("oauth2: cannot fetch token: 502\rBad Gateway\nResponse: <html>\n<body>\x1b[31mdown</body>")
	pressAndRun(s, "enter")
	s.resize(200, 30)
	want := "  linear  ✗ login failed: oauth2: cannot fetch token: 502Bad Gateway" + oneLoginLeft + "\n\n> Continue"
	if text := loginsText(s); text != want {
		t.Errorf("screen:\n%q\nwant the error's first line, without control characters, on linear's row", text)
	}
}

func TestLoginsScreen_aFailedLoginSaysToTryAgainOnItsRow(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	logins.ends["linear"] <- errors.New("access denied")
	pressAndRun(s, "enter")
	a := framed(s, false)
	send(a, "up")
	want := "> linear  ✗ login failed: access denied  press enter to try again"
	if text := framedText(a); !strings.Contains(text, want) {
		t.Errorf("screen:\n%s\nwant the failure on linear's row, then the retry hint", text)
	}
}

func TestLoginsScreen_aLongLoginErrorIsCutSoTheRetryHintFits(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	logins.ends["linear"] <- errors.New(`token exchange failed: 400 Bad Request: {"error":"invalid_grant"}`)
	pressAndRun(s, "enter")
	a := framed(s, false)
	send(a, "up")
	row := strings.Split(framedText(a), "\n")[0]
	if !strings.HasPrefix(row, "> linear  ✗ login failed: token") ||
		!strings.HasSuffix(row, "…  press enter to try again") ||
		ansi.StringWidth(row) > 80 {
		t.Errorf("row %q: want the error cut short so the retry hint fits in 80 columns", row)
	}
}

func TestLoginsScreen_aTimedOutLoginSaysSoAndCanBeTriedAgain(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	logins.ends["linear"] <- fmt.Errorf("auth flow: %w", context.DeadlineExceeded)
	pressAndRun(s, "enter")
	a := framed(s, false)
	send(a, "up")
	if text := framedText(a); !strings.Contains(text, "> linear  ✗ login timed out  press enter to try again") {
		t.Errorf("screen:\n%s\nwant the timeout said plainly, with the retry hint on linear's row", text)
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
			initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLoginState{}},
			initcmd.ServerStatus{Name: "sentry", Readiness: initcmd.NeedsLoginState{}},
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
		finish(checks, s, wait, initcmd.ReadyState{})
		if text := loginsText(s); !strings.Contains(text, "> sentry") {
			t.Errorf("screen:\n%s\nwant the cursor still on sentry", text)
		}
	})
	t.Run("on Continue, a new login row doesn't pull it back", func(t *testing.T) {
		checks, s, wait := setup()
		a := framed(s, false)
		send(a, "down", "down")
		finish(checks, s, wait, initcmd.NeedsLoginState{})
		if text := framedText(a); !strings.Contains(text, "> Continue") {
			t.Errorf("screen:\n%s\nwant the cursor still on Continue", text)
		}
	})
	t.Run("unmoved, it rests on the first login to do", func(t *testing.T) {
		checks, s, wait := setup()
		finish(checks, s, wait, initcmd.NeedsLoginState{})
		if text := loginsText(s); !strings.Contains(text, "> open") {
			t.Errorf("screen:\n%s\nwant the cursor on open, now the first login", text)
		}
	})
}

func TestLoginsScreen_aShortWindowKeepsThePendingLoginsURLInView(t *testing.T) {
	logins := newFakeLogins("linear", "sentry", "notion")
	s := loginScreenWithStatuses(logins,
		initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLoginState{}},
		initcmd.ServerStatus{Name: "sentry", Readiness: initcmd.NeedsLoginState{}},
		initcmd.ServerStatus{Name: "notion", Readiness: initcmd.NeedsLoginState{}},
		initcmd.ServerStatus{Name: "github", Readiness: initcmd.NeedsTokenState{}},
	)
	s.resize(30, 30)
	s.handle(press("down"))
	_, cmd := s.handle(press("enter"))
	s.update(cmd())
	t.Cleanup(s.cancelLogin)
	all := strings.Split(ansi.Strip(s.body(0, true)), "\n")
	sentry := slices.IndexFunc(all, func(line string) bool { return strings.HasPrefix(line, "> sentry") })
	if sentry < 0 || sentry+3 > len(all) {
		t.Fatalf("full body:\n%s\nwant sentry's row with its URL wrapped under it", strings.Join(all, "\n"))
	}
	if body, want := ansi.Strip(s.body(3, true)), strings.Join(all[sentry:sentry+3], "\n"); body != want {
		t.Errorf("body at height 3:\n%s\nwant sentry's row and its whole URL:\n%s", body, want)
	}
	if body, want := ansi.Strip(s.body(2, true)), strings.Join(all[sentry:sentry+2], "\n"); body != want {
		t.Errorf("body at height 2:\n%s\nwant sentry's row kept when its URL doesn't fit:\n%s", body, want)
	}
}

func TestLoginsScreen_manyByHandServersAreCutSoTheRowsAndContinueFit(t *testing.T) {
	statuses := []initcmd.ServerStatus{{Name: "linear", Readiness: initcmd.NeedsLoginState{}}}
	for i := range 20 {
		statuses = append(
			statuses,
			initcmd.ServerStatus{Name: fmt.Sprintf("token%d", i), Readiness: initcmd.NeedsTokenState{}},
		)
	}
	s := loginScreenWithStatuses(newFakeLogins(), statuses...)
	a := framed(s, false)
	a.height = minHeight
	view := ansi.Strip(a.render())
	if strings.Count(view, "\n") >= minHeight || !strings.Contains(view, "> linear") ||
		!strings.Contains(view, "  Continue") {
		t.Errorf("view at height %d:\n%s\nwant linear's row and Continue in view, the list cut", minHeight, view)
	}
}

func TestLoginsScreen_whileALoginWaitsNoOtherRowOffersEnter(t *testing.T) {
	logins := newFakeLogins("linear", "sentry")
	s := loginScreen(logins, "linear", "sentry")
	_, cmd := s.handle(press("enter"))
	s.update(cmd())
	t.Cleanup(s.cancelLogin)
	s.handle(press("down"))
	if text := loginsText(s); !strings.Contains(text, "> sentry") || strings.Contains(text, "press enter") {
		t.Errorf("screen:\n%s\nwant the cursor on sentry with no hint: enter starts nothing while linear waits", text)
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
	before := rowsWithoutHints(s.body(4, true))
	s.handle(press("up"))
	after := rowsWithoutHints(s.body(4, true))
	if !slices.Equal(stripMarks(before), stripMarks(after)) || !strings.HasPrefix(after[2], "> s7") {
		t.Errorf("view before up:\n%s\nafter:\n%s\nwant the same rows, the cursor one up on s7",
			strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
}

func rowsWithoutHints(body string) []string {
	var rows []string
	for _, line := range strings.Split(ansi.Strip(body), "\n") {
		row, _, _ := strings.Cut(line, "  press enter")
		rows = append(rows, row)
	}
	return rows
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
		configDirFor: func(string) string { return "testconfig" },
		steps:        testSteps,
		statuses:     func() ([]initcmd.ServerStatus, error) { return nil, errors.New("permission denied") },
		checking:     func() map[string]bool { return nil },
	})
	showScreen(s)
	a := framed(s, true)
	send(a, "down")
	if text := framedText(a); !strings.HasSuffix(text, "permission denied\n\n  Continue\n> Back") {
		t.Errorf("screen:\n%s\nwant the error above Continue and Back, with the cursor on Back", text)
	}
	if heading := s.heading(); heading != "Log in to your MCPs (optional)" {
		t.Errorf("heading = %q, want the screen's own heading, not one about servers it couldn't read", heading)
	}
}

func TestLoginsScreen_tabJumpsToContinueAndUpReturnsToTheServerItLeft(t *testing.T) {
	s := newFakeChecks(
		initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLoginState{}},
		initcmd.ServerStatus{Name: "sentry", Readiness: initcmd.NeedsLoginState{}},
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
		initcmd.ServerStatus{Name: "linear", Readiness: initcmd.NeedsLoginState{}},
		initcmd.ServerStatus{Name: "sentry", Readiness: initcmd.NeedsLoginState{}},
		initcmd.ServerStatus{Name: "notion", Readiness: initcmd.NeedsLoginState{}},
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
	); !strings.Contains(body, "srv11  ✗ login failed: access denied") ||
		!strings.Contains(body, "> Continue") {
		t.Errorf("body at height 8:\n%s\nwant the failed srv11 in view above Continue", body)
	}
}

func waitingLogin(t *testing.T, copy func(string) error) (*loginsScreen, *app) {
	t.Helper()
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	s.p.copy = copy
	_, cmd := s.handle(press("enter"))
	s.update(cmd())
	t.Cleanup(s.cancelLogin)
	return s, framed(s, false)
}

func TestLoginsScreen_cCopiesTheWholeLinkAndTheFooterSaysSo(t *testing.T) {
	var copied string
	s, a := waitingLogin(t, func(text string) error { copied = text; return nil })
	deliver(a, send(a, "c"))
	if copied != "https://auth.example/linear" || !strings.HasPrefix(a.keys(s), "✓ link copied · ") {
		t.Fatalf("copied %q, keys %q; want the whole URL copied and the footer saying so", copied, a.keys(s))
	}
	send(a, "up")
	if strings.Contains(a.keys(s), "copied") {
		t.Errorf("keys after another key = %q, want the notice gone", a.keys(s))
	}
}

func TestLoginsScreen_aCopyThatCantBeConfirmedAsksTheTerminal(t *testing.T) {
	for name, err := range map[string]error{
		"no clipboard tool": errNoClipboardTool,
		"a failing tool":    errors.New("xclip: exit status 1"),
	} {
		t.Run(name, func(t *testing.T) {
			s, a := waitingLogin(t, func(string) error { return err })
			_, cmd := a.Update(press("c"))
			terminalCopy := s.update(cmd())
			if terminalCopy == nil || !strings.HasPrefix(a.keys(s), "asked the terminal to copy · ") {
				t.Errorf("command %v, keys %q; want the terminal asked, and the footer not claiming it worked",
					terminalCopy, a.keys(s))
			}
		})
	}
}

func TestLoginsScreen_cIsOfferedOnlyOnceTheLoginHasALink(t *testing.T) {
	s := loginScreen(newFakeLogins("linear"), "linear")
	_, cmd := s.handle(press("enter"))
	t.Cleanup(s.cancelLogin)
	if strings.Contains(s.keys(), "copy") {
		t.Errorf("keys before the link arrives = %q, want no copy: there is nothing to copy yet", s.keys())
	}
	s.update(cmd())
	if !strings.Contains(s.keys(), "c copy link") {
		t.Errorf("keys once the link arrived = %q, want c copy link", s.keys())
	}
}

func TestLoginsScreen_aCopyThatFinishesAfterItsLoginEndedSaysNothing(t *testing.T) {
	s, a := waitingLogin(t, func(string) error { return errNoClipboardTool })
	_, cmd := a.Update(press("c"))
	copied := cmd()
	send(a, "esc")
	if terminalCopy := s.update(copied); terminalCopy != nil || strings.Contains(a.keys(s), "copy") {
		t.Errorf("command %v, keys %q; want nothing for a login that is no longer waiting", terminalCopy, a.keys(s))
	}
}

func TestLoginsScreen_whileALoginWaitsTheFooterGivesEscOneMeaning(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	_, cmd := s.handle(press("enter"))
	s.update(cmd())
	t.Cleanup(s.cancelLogin)
	a := framed(s, true)
	a.width, a.height = 120, 30
	if footer := footerOf(
		shown(a),
	); !strings.Contains(footer, "esc cancel login") ||
		strings.Contains(footer, "esc back") {
		t.Errorf("footer:\n%s\nwant esc named once, as cancelling the login", footer)
	}
}

func TestRun_aByHandServerPointsAtTheFileItWillHaveInMini(t *testing.T) {
	configDir := t.TempDir()
	setup := setupFor(configDir)
	setup.Probe = func(_ context.Context, dir string, sc config.ServerConfig) error {
		return config.MarkOAuthDetected(dir, sc.Name)
	}
	setup.Discover = func(context.Context, string) (*auth.ServerMeta, error) { return &auth.ServerMeta{}, nil }
	token := catalog.Entry{Name: "github", URL: "https://mcp.github.example/mcp", Auth: catalog.AuthToken}
	var view string
	program := func(m tea.Model) error {
		pressingAndDelivering("space", "tab", "enter")(m)
		view = shown(m.(*app))
		return nil
	}
	if _, err := Run(
		Params{
			Setup:       setup,
			LoadCatalog: fromCatalog(catalog.Catalog{Entries: []catalog.Entry{token}}),
			Program:     program,
		},
	); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configDir, "servers", "github.yaml")
	if !strings.Contains(strings.Join(strings.Fields(view), ""), want) {
		t.Errorf(
			"Logins:\n%s\nwant github's step to name %s, where Save and finish puts it, not the staging copy",
			view,
			want,
		)
	}
}
