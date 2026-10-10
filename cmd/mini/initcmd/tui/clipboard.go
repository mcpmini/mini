package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var errNoClipboardTool = errors.New("no clipboard tool found")

// A clipboard tool that hangs, such as xclip with no X server to reach, would leave the copy unanswered.
const clipboardTimeout = 2 * time.Second

func copyLinkToClipboard(link string) error {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	return copyToClipboard(ctx, link)
}

// copyToClipboard runs the platform's own clipboard tools in turn, so the copy reports whether it
// worked; a terminal asked to copy over its escape codes never says.
func copyToClipboard(ctx context.Context, text string) error {
	// Over SSH the tools would fill the remote machine's clipboard, not the user's.
	if os.Getenv("SSH_CONNECTION") != "" {
		return errNoClipboardTool
	}
	err := errNoClipboardTool
	for _, tool := range clipboardTools() {
		path, lookErr := exec.LookPath(tool[0])
		if lookErr != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, path, tool[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err = cmd.Run(); err == nil {
			return nil
		}
	}
	return err
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
