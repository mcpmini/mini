//go:build !windows

package main

import "os/exec"

// shellOpen runs browserCmd through sh so that quoted args (e.g. 'Google Chrome')
// are parsed correctly. URL is passed as $1 to avoid shell injection.
func shellOpen(browserCmd, url string) error {
	return exec.Command("sh", "-c", browserCmd+` "$1"`, "--", url).Start()
}
