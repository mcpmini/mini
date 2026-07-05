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
		return startAndReap(exec.Command(parts[0], append(parts[1:], url)...))
	}
	return startAndReap(exec.Command("sh", "-c", browserCmd+` "$1"`, "--", url))
}

func openPlatformDefault(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return startAndReap(exec.Command("open", url))
	case "linux":
		return startAndReap(exec.Command("xdg-open", url))
	case "windows":
		return startAndReap(exec.Command("cmd", "/c", "start", url))
	default:
		return nil
	}
}

func startAndReap(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
