package configui

import "errors"

type Backend interface {
	// Servers skips names that are no longer in mini, so a session never lists a server it can't show.
	Servers(names []string) []ServerSummary
	Server(name string) (ServerView, error)
}

var ErrUnknownServer = errors.New("unknown server")

type State string

const (
	StateConnecting State = "connecting"
	StateReady      State = "ready"
	StateNeedsAuth  State = "needs_auth"
	StateFailed     State = "failed"
)

type ServerSummary struct {
	Name  string `json:"name"`
	State State  `json:"state"`
}

type ServerView struct {
	Name  string `json:"name"`
	State State  `json:"state"`
	Error string `json:"error,omitempty"`
	Tools []Tool `json:"tools"`
}

type Tool struct {
	Name   string `json:"name"`
	Hidden bool   `json:"hidden"`
}
