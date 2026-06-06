package auth

import (
	"os/exec"
	"runtime"
	"strings"
)

// OpenBrowser opens url in the browser specified by browserCmd, falling back
// to the platform default when browserCmd is empty. On non-Windows, browserCmd
// is passed through sh so quoted arguments (e.g. "Google Chrome") work.
func OpenBrowser(browserCmd, url string) error {
	if browserCmd != "" {
		return openWithCmd(browserCmd, url)
	}
	return openPlatformDefault(url)
}

func openWithCmd(browserCmd, url string) error {
	if runtime.GOOS == "windows" {
		parts := strings.Fields(browserCmd)
		if len(parts) == 0 {
			return nil
		}
		return exec.Command(parts[0], append(parts[1:], url)...).Start()
	}
	return exec.Command("sh", "-c", browserCmd+` "$1"`, "--", url).Start()
}

func openPlatformDefault(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start()
	default:
		return nil
	}
}
