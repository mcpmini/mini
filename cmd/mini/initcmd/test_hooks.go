//go:build test

package initcmd

import "testing"

// UseTemporaryDirs makes init count only dirs as temporary until the test ends.
func UseTemporaryDirs(t testing.TB, dirs ...string) {
	saved := temporaryDirs
	temporaryDirs = func() []string { return dirs }
	t.Cleanup(func() { temporaryDirs = saved })
}
