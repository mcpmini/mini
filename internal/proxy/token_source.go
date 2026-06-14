package proxy

import "sync"

type tokenSource struct {
	mu     sync.Mutex
	value  string
	reload func() (string, error)
}

func (ts *tokenSource) current() string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.value
}

// refresh re-reads the token; a failed reload keeps the current value so a transient
// read error doesn't blank the token and turn a recoverable 401 into a hard failure.
func (ts *tokenSource) refresh() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.reload == nil {
		return
	}
	if token, err := ts.reload(); err == nil {
		ts.value = token
	}
}
