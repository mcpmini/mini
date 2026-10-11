package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
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

func listAllServers(configDir string, out io.Writer) error {
	_, servers, err := loadConfig(configDir)
	if err != nil {
		return err
	}
	warnServerProblems(os.Stderr, servers)
	if noServers(servers) {
		_, err := fmt.Fprintln(out, noServersConfigured)
		return err
	}
	return printServerTable(out, servers.Loaded)
}

func noServers(servers config.Servers) bool {
	return len(servers.Loaded) == 0 && len(servers.Broken) == 0
}

func printServerTable(out io.Writer, servers []config.ServerConfig) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tTRANSPORT\tCOMMAND / URL\tENABLED"); err != nil {
		return fmt.Errorf("write server table header: %w", err)
	}
	for _, sc := range servers {
		if _, err := fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\n",
			sc.Name,
			serverTransport(sc),
			serverTarget(sc),
			enabledStr(sc),
		); err != nil {
			return fmt.Errorf("write server table row: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush server table: %w", err)
	}
	return nil
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
	for _, se := range p.Servers.Broken {
		if _, err := fmt.Fprintf(
			w,
			"%s\t%s\terror: %s\t-\n",
			se.ServerName,
			unknownTransport,
			singleLine(se.Err),
		); err != nil {
			return false, fmt.Errorf("write broken server row: %w", err)
		}
	}
	anyFailed := len(p.Servers.Broken) > 0
	for _, sc := range p.Servers.Loaded {
		failed, err := printStatusRow(statusRowParams{Context: p.Context, Writer: w, Server: p.Server, Config: sc})
		if err != nil {
			return anyFailed, err
		}
		anyFailed = failed || anyFailed
	}
	if err := w.Flush(); err != nil {
		return anyFailed, fmt.Errorf("flush status table: %w", err)
	}
	return anyFailed, nil
}

func projectionsNote(sc config.ServerConfig) string {
	if sc.ProjectionsErr == nil {
		return ""
	}
	return ", " + singleLine(projectionsError(sc))
}

type statusRowParams struct {
	Context context.Context
	Writer  io.Writer
	Server  *server.Server
	Config  config.ServerConfig
}

func printStatusRow(p statusRowParams) (bool, error) {
	status, tools := "disabled"+projectionsNote(p.Config), "-"
	failed := p.Config.ProjectionsErr != nil
	if p.Config.IsEnabled() {
		if err := p.Server.AddUpstream(p.Context, p.Config); err != nil {
			status, failed = "error: "+singleLine(err), true
		} else {
			status = "ok" + projectionsNote(p.Config)
			tools = strconv.Itoa(p.Server.ToolCount(p.Config.Name))
		}
	}
	_, err := fmt.Fprintf(p.Writer, "%s\t%s\t%s\t%s\n", p.Config.Name, serverTransport(p.Config), status, tools)
	if err != nil {
		return failed, fmt.Errorf("write status row: %w", err)
	}
	return failed, nil
}
