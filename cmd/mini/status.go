package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newLsCmd(configDir string) *cobra.Command {
	return &cobra.Command{
		Use:                "ls [SERVER] [TOOL]",
		Aliases:            []string{"list"},
		Short:              "List servers, server tools, or tool detail",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(configDir, args, cmd.OutOrStdout())
		},
	}
}

func newStatusCmd(configDir string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show server health",
		RunE: func(cmd *cobra.Command, args []string) error {
			runStatus(configDir)
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
	_, servers, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if len(servers) == 0 {
		fmt.Fprintln(out, "no servers configured")
		return nil
	}
	printServerTable(out, servers)
	return nil
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
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fatalf("load config: %v", err)
	}
	if len(servers) == 0 {
		fmt.Println("no servers configured")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	injectOAuthTokens(ctx, configDir, servers)
	srv := buildStatusServer(cfg, configDir)
	defer srv.Close()
	printStatusTable(ctx, srv, servers)
}

func buildStatusServer(cfg *config.Config, configDir string) *server.Server {
	cfg.ResponseDir = filepath.Join(configDir, "internal", "responses")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return server.NewWithConfigDir(cfg, configDir, logger)
}

func printStatusTable(ctx context.Context, srv *server.Server, servers []config.ServerConfig) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTRANSPORT\tSTATUS\tTOOLS")
	anyFailed := false
	for _, sc := range servers {
		anyFailed = printStatusRow(ctx, w, srv, sc) || anyFailed
	}
	w.Flush()
	if anyFailed {
		os.Exit(1)
	}
}

func printStatusRow(ctx context.Context, w *tabwriter.Writer, srv *server.Server, sc config.ServerConfig) bool {
	if !sc.IsEnabled() {
		fmt.Fprintf(w, "%s\t-\tdisabled\t-\n", sc.Name)
		return false
	}
	t := serverTransport(sc)
	if err := srv.AddUpstream(ctx, sc); err != nil {
		fmt.Fprintf(w, "%s\t%s\terror: %v\t-\n", sc.Name, t, err)
		return true
	}
	fmt.Fprintf(w, "%s\t%s\tok\t%d\n", sc.Name, t, srv.ToolCount(sc.Name))
	return false
}
