//go:build !unix

package main

import "os/exec"

// Platforms without Unix process groups retain CommandContext's process-level cancellation.
func ownProcessGroup(_ *exec.Cmd) func() { return func() {} }
