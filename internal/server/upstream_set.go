package server

import (
	"slices"
	"sync"
)

type upstreamSet struct {
	mu     sync.RWMutex // a leaf: nothing calls into an incarnation while holding it
	byName map[string]*upstreamServer
}

func newUpstreamSet() *upstreamSet {
	return &upstreamSet{byName: make(map[string]*upstreamServer)}
}

func (s *upstreamSet) get(name string) *upstreamServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byName[name]
}

func (s *upstreamSet) snapshot() []*upstreamServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	upstreams := make([]*upstreamServer, 0, len(s.byName))
	for _, u := range s.byName {
		upstreams = append(upstreams, u)
	}
	return upstreams
}

func (s *upstreamSet) configNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.byName))
	for name, u := range s.byName {
		if u.fromConfig {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func (s *upstreamSet) swap(name string, next *upstreamServer) (prev *upstreamServer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev = s.byName[name]
	if next == nil {
		delete(s.byName, name)
	} else {
		s.byName[name] = next
	}
	return prev
}

// broadcast wakes everyone waiting on the channel taken before a change. Taking the channel before
// reading the state it guards means a waiter never misses a change made after its read.
type broadcast struct {
	mu sync.Mutex
	ch chan struct{}
}

func newBroadcast() *broadcast {
	return &broadcast{ch: make(chan struct{})}
}

func (b *broadcast) wait() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

func (b *broadcast) signal() {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.ch)
	b.ch = make(chan struct{})
}
