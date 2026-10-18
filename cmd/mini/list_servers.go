package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type serverListRow struct {
	Config config.ServerConfig
	State  string
}

func listAllServers(configDir string, out io.Writer) error {
	cfg, servers, err := loadConfig(configDir)
	if err != nil {
		return err
	}
	if noServers(servers) {
		_, err = fmt.Fprintln(out, noServersConfigured)
		return err
	}
	rows := checkServerList(configDir, cfg, servers)
	if err = printServerTable(out, rows); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "\nTools: mini ls <server>\nHealth details: mini status")
	return err
}

func checkServerList(configDir string, cfg *config.Config, servers config.Servers) []serverListRow {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv := buildStatusServer(cfg, configDir)
	defer srv.Close()
	return probeServerList(ctx, srv, servers)
}

func probeServerList(ctx context.Context, srv *server.Server, servers config.Servers) []serverListRow {
	rows := make([]serverListRow, len(servers.Broken)+len(servers.Loaded))
	for i, broken := range servers.Broken {
		rows[i] = serverListRow{Config: config.ServerConfig{Name: broken.ServerName}, State: "invalid"}
	}
	var probes sync.WaitGroup
	for i, sc := range servers.Loaded {
		probes.Go(func() {
			rows[len(servers.Broken)+i] = probeListedServer(ctx, srv, sc)
		})
	}
	probes.Wait()
	return rows
}

func probeListedServer(ctx context.Context, srv *server.Server, sc config.ServerConfig) serverListRow {
	if !sc.IsEnabled() {
		return serverListRow{Config: sc, State: "disabled"}
	}
	if err := srv.AddUpstream(ctx, sc); err != nil {
		return serverListRow{Config: sc, State: connectionState(err)}
	}
	return serverListRow{Config: sc, State: "ready"}
}

func connectionState(err error) string {
	var unauthorized *transport.UnauthorizedError
	if errors.Is(err, transport.ErrReauthRequired) || errors.As(err, &unauthorized) {
		return "needs-auth"
	}
	return "failed"
}

func printServerTable(out io.Writer, rows []serverListRow) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tTYPE\tSTATE\tURL / COMMAND"); err != nil {
		return fmt.Errorf("write server table header: %w", err)
	}
	for _, row := range rows {
		if err := printListedServer(w, row); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush server table: %w", err)
	}
	return nil
}

func printListedServer(out io.Writer, row serverListRow) error {
	kind := serverTransport(row.Config)
	if row.State == "invalid" {
		kind = unknownTransport
	}
	_, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", row.Config.Name, kind, row.State, serverTarget(row.Config))
	return err
}
