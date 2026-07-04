package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/version"
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "mini: %v\n", err)
		os.Exit(exitCodeFor(err))
	}
}

type rootOptions struct {
	configDir string
}

func newRootCmd() *cobra.Command {
	opts := &rootOptions{configDir: config.DefaultConfigDir()}
	root := &cobra.Command{
		Use:     "mini [--config DIR]",
		Short:   "mini connects agents to MCP servers",
		Version: version.Version,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErrf("%v", err) })
	root.PersistentFlags().StringVar(&opts.configDir, "config", opts.configDir, "config directory")
	root.AddCommand(subcommands(opts)...)
	return root
}

func subcommands(opts *rootOptions) []*cobra.Command {
	return []*cobra.Command{
		newConnectCmd(opts),
		newDaemonCmd(opts),
		newLsCmd(opts),
		newAddCmd(opts),
		newRmCmd(opts),
		newStatusCmd(opts),
		newCleanupCmd(opts),
		newAuthCmd(opts),
		newTestCmd(opts),
		newInitCmd(opts),
		newCallCmd(opts),
		newPermCallCmd(opts),
		newVersionCmd(),
	}
}

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return usageErrf("%v", err)
		}
		return nil
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
