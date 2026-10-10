package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type pendingLogin struct {
	id   int
	name string
	url  string
	// revealed shows the whole link once a copy can't confirm it worked, so it can be selected by hand.
	revealed bool
	cancel   context.CancelFunc
	done     chan struct{}
	// Sized for everything runLogin sends, so cancelling and waiting on done never blocks on the UI.
	events chan tea.Msg
}

type loginStarted struct {
	id  int
	url string
}

type loginFinished struct {
	id  int
	err error
}

func (s *loginsScreen) startLogin(name string) tea.Cmd {
	if s.pending != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.logins++
	login := &pendingLogin{
		id: s.logins, name: name, cancel: cancel, done: make(chan struct{}), events: make(chan tea.Msg, 2),
	}
	s.pending = login
	go s.runLogin(ctx, login)
	return login.next
}

func (s *loginsScreen) runLogin(ctx context.Context, login *pendingLogin) {
	defer close(login.done)
	started, err := s.p.startLogin(ctx, login.name)
	if err != nil {
		login.events <- loginFinished{id: login.id, err: err}
		return
	}
	login.events <- loginStarted{id: login.id, url: started.URL}
	login.events <- loginFinished{id: login.id, err: started.Wait()}
}

func (login *pendingLogin) next() tea.Msg {
	return <-login.events
}

func (s *loginsScreen) current(id int) bool {
	return s.pending != nil && s.pending.id == id
}

func (s *loginsScreen) loginStarted(msg loginStarted) tea.Cmd {
	if !s.current(msg.id) {
		return nil
	}
	s.pending.url = msg.url
	return s.pending.next
}

func (s *loginsScreen) loginFinished(msg loginFinished) {
	// A login cancelled by leaving the screen proved nothing.
	if !s.current(msg.id) {
		return
	}
	s.results[s.pending.name] = msg.err
	s.pending.cancel()
	s.pending = nil
	s.cursorMoved = false
	s.refresh()
}

// cancelLogin waits for the login to stop, so nothing it does outlives the screen.
func (s *loginsScreen) cancelLogin() {
	if s.pending == nil {
		return
	}
	s.pending.cancel()
	<-s.pending.done
	s.pending = nil
}

type linkCopied struct {
	id  int
	err error
}

func (s *loginsScreen) copyLink() tea.Cmd {
	if s.pending == nil || s.pending.url == "" {
		return nil
	}
	id, link := s.pending.id, s.pending.url
	return func() tea.Msg {
		return linkCopied{id: id, err: s.p.copy(link)}
	}
}

// Without a working clipboard tool, as over SSH, the terminal is asked to copy instead. It never says
// whether it did, so the notice doesn't claim it and the whole link is shown to select by hand.
func (s *loginsScreen) linkCopied(msg linkCopied) tea.Cmd {
	if !s.current(msg.id) {
		return nil
	}
	if msg.err == nil {
		s.notice = "✓ link copied"
		return nil
	}
	s.notice, s.pending.revealed = "asked the terminal to copy the link", true
	return tea.SetClipboard(s.pending.url)
}
