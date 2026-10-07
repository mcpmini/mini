package tui

import (
	"context"
	"errors"
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
	want := "> linear  needs a login\n  github  needs a token\n" +
		"  asana   needs your own OAuth app\n  files   needs ROOT set\n  Continue →"
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
	if text := loginsText(s); !strings.Contains(text, "  open  checking…") || wait == nil {
		t.Fatalf("screen:\n%s\nwait = %v; want open checking and a wait for the result", text, wait)
	}
	if again := s.enter(); again != nil {
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
	wait := s.enter()
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
	wait := s.enter()
	delete(checks.checking, "open")
	checks.changed <- struct{}{}
	s.update(wait())
	if s.heading() != "Your servers are ready" ||
		loginsText(s) != "Every server works; nothing is left to set up.\n\n> Continue →" {
		t.Errorf("%s\n%s\nwant the screen to say every server is ready, not an empty list", s.heading(), loginsText(s))
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
	s.resize(80)
	s.enter()
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
	if text := loginsText(s); !strings.Contains(text, "  sentry  ✗ access denied\n> Continue →") {
		t.Errorf("screen:\n%s\nwant sentry's failure shown and the cursor on Continue", text)
	}
}

func TestLoginsScreen_leavingCancelsThePendingLoginAndDropsItsResult(t *testing.T) {
	logins := newFakeLogins("linear")
	s := loginScreen(logins, "linear")
	_, cmd := s.handle(press("enter"))
	s.update(cmd())

	if move, _ := s.handle(press("esc")); move != back {
		t.Fatalf("esc = %v, want back", move)
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
	s.resize(40)
	_, cmd := s.handle(press("enter"))
	s.update(cmd())

	body := s.body(40)
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
	if text := loginsText(s); text != "  linear  ✗ oauth2: cannot fetch token: 502Bad Gateway\n> Continue →" {
		t.Errorf("screen:\n%q\nwant the error's first line, without control characters, on linear's row", text)
	}
}
