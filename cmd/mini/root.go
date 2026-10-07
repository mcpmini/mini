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
	configDir        string
	defaultConfigErr error
}

func newRootCmd() *cobra.Command {
	opts := rootOptionsForDefaults()
	root := &cobra.Command{
		Use:     "mini [--config DIR]",
		Short:   "mini connects agents to MCP servers",
		Long:    "mini connects agents to MCP servers and trims their responses.\n\nNew here? Run `mini init` to import servers from your other tools and pick more from the server catalog.",
		Version: version.Version,
	}
	configureRootCmd(root, opts)
	return root
}

func rootOptionsForDefaults() *rootOptions {
	dir, err := config.DefaultConfigDir()
	return &rootOptions{configDir: dir, defaultConfigErr: err}
}

func configureRootCmd(root *cobra.Command, opts *rootOptions) {
	root.SetVersionTemplate("{{.Version}}\n")
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErrf("%v", err) })
	root.PersistentFlags().StringVar(&opts.configDir, "config", opts.configDir, "config directory")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if opts.defaultConfigErr == nil || opts.configDir != "" || configFreeCommand(cmd) {
			return nil
		}
		return fmt.Errorf("cannot determine default config directory; pass --config DIR: %w", opts.defaultConfigErr)
	}
	root.AddCommand(subcommands(opts)...)
}

func configFreeCommand(cmd *cobra.Command) bool {
	return cmd.Name() == "help" || cmd.Name() == "version" || cmd.Flags().Changed("version")
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
