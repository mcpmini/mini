//go:build test

package auth

import (
	"slices"
	"testing"
)

func TestUnixBrowserCommand_URLRemainsSeparateFromShellCommand(t *testing.T) {
	browserCmd := `browser --profile "work profile"`
	url := "http://example.com?a=1&b=$(echo injected)&c=hello world"

	cmd := unixBrowserCommand(browserCmd, url)

	want := []string{"sh", "-c", browserCmd + ` "$1"`, "--", url}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("browser arguments = %q, want %q", cmd.Args, want)
	}
}
