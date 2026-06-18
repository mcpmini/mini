//go:build windows

package daemon

// No-op on Windows: the OS socket bind guarantees a single daemon; this lock only collapses wasted spawn attempts (see spawnlock_unix.go).
func acquireSpawnLock(_ string) (release func(), err error) {
	return func() {}, nil
}
