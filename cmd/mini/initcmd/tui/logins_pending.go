package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type pendingLogin struct {
	id     int
	name   string
	url    string
	cancel context.CancelFunc
	done   chan struct{}
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
