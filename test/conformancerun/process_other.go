//go:build !unix

package main

import "os/exec"

func ownProcessGroup(_ *exec.Cmd) func() { return func() {} }
