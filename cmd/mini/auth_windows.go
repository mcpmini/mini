//go:build windows

package main

import (
	"os/exec"
	"strings"
)

// shellOpen on Windows splits on whitespace and appends the URL.
// Windows browser_cmd values typically don't use shell quoting.
func shellOpen(browserCmd, url string) error {
	parts := strings.Fields(browserCmd)
	return exec.Command(parts[0], append(parts[1:], url)...).Start()
}
