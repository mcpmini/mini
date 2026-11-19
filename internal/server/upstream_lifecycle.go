package server

// beginStop stops the incarnation publishing or starting work, and cancels what runs. Its connection
// stays open until closeConn: HTTP Close waits for the notification listener, which may be in a
// token refresh that only ending the provider stops.
func (u *upstreamServer) beginStop() {
	u.mu.Lock()
	u.group.markStopped()
	u.mu.Unlock()
	u.refresher.stop()
	u.cancel()
}

func (u *upstreamServer) closeConn() {
	u.mu.RLock()
	conn := u.conn
	u.mu.RUnlock()
	// Outside u.mu: HTTP Close waits for notification callbacks that take u.mu.
	if conn != nil {
		conn.Close() //nolint:errcheck // Stopping has no caller to receive connection cleanup errors.
	}
}

func (u *upstreamServer) awaitStopped() { u.group.Wait() }

// stop ends an incarnation another one replaces: its provider is the new one's, so it stays.
func (u *upstreamServer) stop() {
	u.beginStop()
	u.closeConn()
	u.awaitStopped()
}
