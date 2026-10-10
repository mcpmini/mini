package tui

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var errNoClipboardTool = errors.New("no clipboard tool found")

// A clipboard tool that hangs, such as xclip with no X server to reach, would leave the copy unanswered.
const clipboardTimeout = 2 * time.Second

// copyToClipboard runs the platform's own clipboard tool, so the copy reports whether it worked;
// a terminal asked to copy over its escape codes never says.
func copyToClipboard(text string) error {
	path, args, ok := findClipboardTool()
	if !ok {
		return errNoClipboardTool
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func findClipboardTool() (path string, args []string, ok bool) {
	for _, tool := range clipboardTools() {
		if path, err := exec.LookPath(tool[0]); err == nil {
			return path, tool[1:], true
		}
	}
	return "", nil, false
}

func clipboardTools() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "windows":
		return [][]string{{"clip"}}
	}
	return [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
}
