package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

const noServersConfigured = "no servers configured; run `mini init` to choose from the MCP catalog, or `mini add NAME --url URL`"

func newLsCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "ls [SERVER] [TOOL]",
		Aliases: []string{"list"},
		Short:   "List servers, server tools, or tool detail",
		Long:    "List configured servers and check their connections. With SERVER, list tools and MCP annotation hints. With SERVER and TOOL, show the description and arguments.",
		Example: "  mini ls\n  mini ls notion\n  mini ls notion notion-search",
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
			return runStatus(opts.configDir, cmd.OutOrStdout())
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

func noServers(servers config.Servers) bool {
	return len(servers.Loaded) == 0 && len(servers.Broken) == 0
}

func serverTransport(sc config.ServerConfig) string {
	if sc.Transport == "" {
		return "stdio"
	}
	return string(sc.Transport)
}

func serverTarget(sc config.ServerConfig) string {
	if sc.URL != "" {
		return sc.URL
	}
	return sc.Command
}

func runStatus(configDir string, out io.Writer) error {
	cfg, servers, err := loadConfig(configDir)
	if err != nil {
		return err
	}
	if noServers(servers) {
		_, writeErr := fmt.Fprintln(out, noServersConfigured)
		return writeErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	injectOAuthTokens(ctx, configDir, servers.Loaded)
	srv := buildStatusServer(cfg, configDir)
	defer srv.Close()
	anyFailed, err := printStatusTable(statusTableParams{Context: ctx, Server: srv, Out: out, Servers: servers})
	if err != nil {
		return err
	}
	if anyFailed {
		return fmt.Errorf("one or more servers are unhealthy")
	}
	return nil
}

func buildStatusServer(cfg *config.Config, configDir string) *server.Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return server.New(server.Params{Config: cfg, ConfigDir: configDir, Logger: logger})
}

type statusTableParams struct {
	Context context.Context
	Server  *server.Server
	Out     io.Writer
	Servers config.Servers
}

func printStatusTable(p statusTableParams) (bool, error) {
	w := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tTRANSPORT\tSTATUS\tTOOLS"); err != nil {
		return false, fmt.Errorf("write status table header: %w", err)
	}
	if err := printBrokenServerRows(w, p.Servers.Broken); err != nil {
		return false, err
	}
	anyFailed := len(p.Servers.Broken) > 0
	rows := probeConcurrently(p.Servers.Loaded, func(sc config.ServerConfig) statusRow {
		return probeStatus(p.Context, p.Server, sc)
	})
	for _, row := range rows {
		if err := printStatusRow(w, row); err != nil {
			return anyFailed, err
		}
		anyFailed = row.failed || anyFailed
	}
	if err := w.Flush(); err != nil {
		return anyFailed, fmt.Errorf("flush status table: %w", err)
	}
	return anyFailed, nil
}

func printBrokenServerRows(w io.Writer, broken []config.SourceError) error {
	for _, se := range broken {
		if _, err := fmt.Fprintf(
			w,
			"%s\t%s\terror: %s\t-\n",
			se.ServerName,
			unknownTransport,
			singleLine(se.Err),
		); err != nil {
			return fmt.Errorf("write broken server row: %w", err)
		}
	}
	return nil
}

func projectionsNote(sc config.ServerConfig) string {
	if sc.ProjectionsErr == nil {
		return ""
	}
	return ", " + singleLine(projectionsError(sc))
}

type statusRow struct {
	config config.ServerConfig
	status string
	tools  string
	failed bool
}

func probeStatus(ctx context.Context, srv *server.Server, sc config.ServerConfig) statusRow {
	row := statusRow{config: sc, status: "disabled" + projectionsNote(sc), tools: "-", failed: sc.ProjectionsErr != nil}
	if !sc.IsEnabled() {
		return row
	}
	if err := srv.AddUpstream(ctx, sc); err != nil {
		row.status, row.failed = "error: "+singleLine(err), true
		return row
	}
	row.status = "ok" + projectionsNote(sc)
	row.tools = strconv.Itoa(srv.ToolCount(sc.Name))
	return row
}

func printStatusRow(w io.Writer, row statusRow) error {
	_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", row.config.Name, serverTransport(row.config), row.status, row.tools)
	if err != nil {
		return fmt.Errorf("write status row: %w", err)
	}
	return nil
}
