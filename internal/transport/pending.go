package transport

import "sync"

// pendingMap tracks in-flight RPC calls by ID, each waiting on a channel.
type pendingMap struct {
	mu sync.Mutex
	m  map[any]chan *Response
}

func newPendingMap() pendingMap {
	return pendingMap{m: make(map[any]chan *Response)}
}

func (p *pendingMap) register(id int64) chan *Response {
	ch := make(chan *Response, 1)
	p.mu.Lock()
	p.m[id] = ch
	p.mu.Unlock()
	return ch
}

func (p *pendingMap) remove(id int64) {
	p.mu.Lock()
	delete(p.m, id)
	p.mu.Unlock()
}

// deliver routes an inbound response to its waiting caller.
// The id is normalized first (JSON numbers arrive as float64).
func (p *pendingMap) deliver(rawID any, resp *Response) {
	id := normalizeID(rawID)
	p.mu.Lock()
	ch, ok := p.m[id]
	if ok {
		delete(p.m, id)
	}
	p.mu.Unlock()
	if ok {
		ch <- resp
	}
}
