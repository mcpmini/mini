//go:build evals

package evals

import "os"

// Env manages temporary directories for a single eval run.
// TempDirs are always removed on Cleanup. DebugDirs are kept on failure.
type Env struct {
	tmpDirs   []string
	debugDirs []string
}

func NewEnv() *Env { return &Env{} }

// TempDir creates a temp dir that is always removed on Cleanup.
func (e *Env) TempDir() string {
	d, _ := os.MkdirTemp("", "mini-eval-*")
	e.tmpDirs = append(e.tmpDirs, d)
	return d
}

// DebugDir creates a temp dir kept on failure for post-mortem inspection.
// Its path is embedded in ClaudeResult.CallLogDir so errors report the location.
func (e *Env) DebugDir(label string) string {
	d, _ := os.MkdirTemp("", "mini-eval-"+label+"-*")
	e.debugDirs = append(e.debugDirs, d)
	return d
}

// Cleanup removes all temp dirs. On success=false, debug dirs are preserved.
func (e *Env) Cleanup(success bool) {
	for _, d := range e.tmpDirs {
		os.RemoveAll(d) //nolint:errcheck
	}
	if success {
		for _, d := range e.debugDirs {
			os.RemoveAll(d) //nolint:errcheck
		}
	}
}
