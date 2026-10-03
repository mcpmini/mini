package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

const noServersConfigured = "no servers configured; run `mini init` to choose from the server catalog, or `mini add NAME --url URL`"

func newLsCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "ls [SERVER] [TOOL]",
		Aliases: []string{"list"},
		Short:   "List servers, server tools, or tool detail",
		Args:    usageArgs(cobra.MaximumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts.configDir, args, cmd.OutOrStdout())
		},
	}
}

func newStatusCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show server health",
		RunE: func(cmd *cobra.Command, args []string) error {
			runStatus(opts.configDir)
			return nil
		},
	}
}

func runList(configDir string, args []string, out io.Writer) error {
	switch len(args) {
	case 0:
		return listAllServers(configDir, out)
	case 1:
		return listServerTools(configDir, args[0], out)
	case 2:
		return listToolDetail(toolDetailParams{ConfigDir: configDir, ServerName: args[0], ToolName: args[1], Out: out})
	default:
		return usageErrf("usage: mini ls [SERVER [TOOL]]")
	}
}

func listAllServers(configDir string, out io.Writer) error {
	_, servers, err := loadConfig(configDir)
	if err != nil {
		return err
	}
	warnServerProblems(os.Stderr, servers)
	if noServers(servers) {
		fmt.Fprintln(out, noServersConfigured)
		return nil
	}
	printServerTable(out, servers.Loaded)
	return nil
}

func noServers(servers config.Servers) bool {
	return len(servers.Loaded) == 0 && len(servers.Broken) == 0
}

func printServerTable(out io.Writer, servers []config.ServerConfig) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTRANSPORT\tCOMMAND / URL\tENABLED")
	for _, sc := range servers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", sc.Name, serverTransport(sc), serverTarget(sc), enabledStr(sc))
	}
	w.Flush()
}

func serverTransport(sc config.ServerConfig) string {
	if sc.Transport == "" {
		return "stdio"
	}
	return sc.Transport
}

func serverTarget(sc config.ServerConfig) string {
	if sc.URL != "" {
		return sc.URL
	}
	return sc.Command
}

func enabledStr(sc config.ServerConfig) string {
	if sc.IsEnabled() {
		return "yes"
	}
	return "no"
}

func runStatus(configDir string) {
	cfg, servers, err := loadConfig(configDir)
	if err != nil {
		fatalf("%v", err)
	}
	if noServers(servers) {
		fmt.Println(noServersConfigured)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	injectOAuthTokens(ctx, configDir, servers.Loaded)
	srv := buildStatusServer(cfg, configDir)
	defer srv.Close()
	printStatusTable(ctx, srv, servers)
}

func buildStatusServer(cfg *config.Config, configDir string) *server.Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return server.New(server.Params{Config: cfg, ConfigDir: configDir, Logger: logger})
}

func printStatusTable(ctx context.Context, srv *server.Server, servers config.Servers) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTRANSPORT\tSTATUS\tTOOLS")
	for _, se := range servers.Broken {
		fmt.Fprintf(w, "%s\t%s\terror: %s\t-\n", se.ServerName, unknownTransport, singleLine(se.Err))
	}
	anyFailed := len(servers.Broken) > 0
	for _, sc := range servers.Loaded {
		anyFailed = printStatusRow(ctx, w, srv, sc) || anyFailed
	}
	w.Flush()
	if anyFailed {
		os.Exit(1)
	}
}

func projectionsNote(sc config.ServerConfig) string {
	if sc.ProjectionsErr == nil {
		return ""
	}
	return ", " + singleLine(projectionsError(sc))
}

func printStatusRow(ctx context.Context, w *tabwriter.Writer, srv *server.Server, sc config.ServerConfig) bool {
	t := serverTransport(sc)
	if !sc.IsEnabled() {
		fmt.Fprintf(w, "%s\t%s\tdisabled%s\t-\n", sc.Name, t, projectionsNote(sc))
		return sc.ProjectionsErr != nil
	}
	if err := srv.AddUpstream(ctx, sc); err != nil {
		fmt.Fprintf(w, "%s\t%s\terror: %s\t-\n", sc.Name, t, singleLine(err))
		return true
	}
	fmt.Fprintf(w, "%s\t%s\tok%s\t%d\n", sc.Name, t, projectionsNote(sc), srv.ToolCount(sc.Name))
	return sc.ProjectionsErr != nil
}
