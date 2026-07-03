package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/version"
)

func main() {
	configDir, rest := extractConfigDir(os.Args[1:])
	root := newRootCmd(configDir)
	root.SetArgs(rest)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "mini: %v\n", err)
		os.Exit(exitCodeFor(err))
	}
}

// --config is resolved here, not via cobra's PersistentFlags, because
// DisableFlagParsing (set on several subcommands below) silently skips
// inherited persistent-flag parsing too — a cobra-declared --config would be
// accepted but discarded on those commands. Stops at "--" so that terminator
// keeps its usual end-of-options meaning for whoever parses the remainder.
func extractConfigDir(args []string) (configDir string, rest []string) {
	configDir = config.DefaultConfigDir()
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if a == "--config" && i+1 < len(args) {
			configDir = args[i+1]
			i++
			continue
		}
		if v, ok := strings.CutPrefix(a, "--config="); ok {
			configDir = v
			continue
		}
		rest = append(rest, a)
	}
	return configDir, rest
}

// helpRequested checks for a bare -h/--help token. DisableFlagParsing commands
// never reach cobra's own help handling, so each must check for this itself
// before treating args as positionals. Stops at "--" so an escaped literal
// -h/--help positional isn't misread as a help request.
func helpRequested(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func newRootCmd(configDir string) *cobra.Command {
	root := &cobra.Command{
		Use:     "mini",
		Short:   "mini connects agents to MCP servers",
		Version: version.Version,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().String("config", config.DefaultConfigDir(), "config directory")
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErrf("%v", err) })
	root.AddCommand(subcommands(configDir)...)
	return root
}

func subcommands(configDir string) []*cobra.Command {
	return []*cobra.Command{
		newConnectCmd(configDir),
		newDaemonCmd(configDir),
		newLsCmd(configDir),
		newAddCmd(configDir),
		newRmCmd(configDir),
		newStatusCmd(configDir),
		newCleanupCmd(configDir),
		newAuthCmd(configDir),
		newTestCmd(configDir),
		newInitCmd(configDir),
		newCallCmd(configDir),
		newPermCallCmd(configDir),
		newVersionCmd(),
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(version.Version)
			return nil
		},
	}
}
