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
	nl := l.entryLocked(name)
	nl.refs++
	l.mu.Unlock()
	nl.Lock()
	return func() { l.release(name, nl) }
}

func (l *nameLocks) tryLock(name string) (unlock func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	nl := l.entryLocked(name)
	// A held lock has refs > 0, so failing here leaves no entry behind.
	if !nl.TryLock() {
		return nil, false
	}
	nl.refs++
	return func() { l.release(name, nl) }, true
}

func (l *nameLocks) entryLocked(name string) *nameLock {
	if l.locks == nil {
		l.locks = make(map[string]*nameLock)
	}
	nl := l.locks[name]
	if nl == nil {
		nl = &nameLock{}
		l.locks[name] = nl
	}
	return nl
}

func (l *nameLocks) release(name string, nl *nameLock) {
	nl.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if nl.refs--; nl.refs == 0 {
		delete(l.locks, name)
	}
}
