//go:build windows

package daemon

// acquireSpawnLock is a no-op on Windows. The cross-process spawn lock only collapses the
// thundering herd of wasted daemon spawns at scale; the OS socket bind in startDaemonHTTP
// still guarantees a single live daemon here, so Windows simply tolerates the extra
// short-lived spawns that lose the port bind and exit. A portable flock equivalent (an
// exclusive LockFileEx on the lock handle) could be added later if the wasted spawns matter.
func acquireSpawnLock(_ string) (release func(), err error) {
	return func() {}, nil
}
