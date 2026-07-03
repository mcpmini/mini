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

// --config is resolved manually rather than via cobra's PersistentFlags: several
// subcommands set DisableFlagParsing to avoid misreading arbitrary server/tool
// names as flags, which silently disables cobra's own persistent-flag parsing too.
func extractConfigDir(args []string) (configDir string, rest []string) {
	configDir = config.DefaultConfigDir()
	i := 0
	for ; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			configDir = args[i+1]
			i++
			continue
		}
		if v, ok := strings.CutPrefix(args[i], "--config="); ok {
			configDir = v
			continue
		}
		break
	}
	return configDir, args[i:]
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
