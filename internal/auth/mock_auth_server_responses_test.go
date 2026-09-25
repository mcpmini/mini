//go:build test

package auth_test

func (m *mockAuthServer) respondWith(status int, body string) {
	m.mu.Lock()
	m.overrideStatus = status
	m.overrideBody = []byte(body)
	m.mu.Unlock()
}
