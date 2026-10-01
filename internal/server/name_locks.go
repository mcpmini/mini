package server

import "sync"

type nameLocks struct {
	mu    sync.Mutex
	locks map[string]*nameLock
}

type nameLock struct {
	sync.Mutex
	refs int
}

func (l *nameLocks) lock(name string) (unlock func()) {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*nameLock)
	}
	nl := l.locks[name]
	if nl == nil {
		nl = &nameLock{}
		l.locks[name] = nl
	}
	nl.refs++
	l.mu.Unlock()
	nl.Lock()
	return func() { l.release(name, nl) }
}

func (l *nameLocks) release(name string, nl *nameLock) {
	nl.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if nl.refs--; nl.refs == 0 {
		delete(l.locks, name)
	}
}
