//go:build windows

package daemon

// acquireSpawnLock is a no-op on Windows: this lock only collapses the wasted-spawn herd
// (see spawnlock_unix.go), and the OS socket bind still guarantees a single daemon here.
func acquireSpawnLock(_ string) (release func(), err error) {
	return func() {}, nil
}
