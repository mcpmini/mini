package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type serverListRow struct {
	Config config.ServerConfig
	Auth   string
	State  string
	Err    error
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
		rows[i] = serverListRow{
			Config: config.ServerConfig{Name: broken.ServerName}, Auth: "-", State: "invalid", Err: broken.Err,
		}
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
	row := serverListRow{Config: sc, Auth: serverAuth(sc), State: "disabled"}
	if sc.ProjectionsErr != nil {
		row.Err = projectionsError(sc)
	}
	if !sc.IsEnabled() {
		return row
	}
	row.State = "ready"
	if err := srv.AddUpstream(ctx, sc); err != nil {
		row.State, row.Err = connectionState(err), err
		if row.Auth == "-" && errors.Is(err, transport.ErrReauthRequired) {
			row.Auth = "oauth"
		}
	}
	return row
}

func connectionState(err error) string {
	var unauthorized *transport.UnauthorizedError
	if errors.Is(err, transport.ErrReauthRequired) || errors.As(err, &unauthorized) {
		return "needs-auth"
	}
	return "failed"
}

func serverAuth(sc config.ServerConfig) string {
	if sc.Auth != nil {
		switch sc.Auth.Type {
		case config.AuthTypeOAuth2:
			return "oauth"
		case config.AuthTypeBearer:
			return "token"
		case config.AuthTypeAPIKey:
			return "api-key"
		}
	}
	for name, value := range sc.Headers {
		if strings.EqualFold(name, "Authorization") && strings.TrimSpace(value) != "" {
			return headerAuthKind(value)
		}
	}
	return "-"
}

func headerAuthKind(value string) string {
	scheme := strings.Fields(value)[0]
	if strings.EqualFold(scheme, "Bearer") {
		return "token"
	}
	if strings.EqualFold(scheme, "Basic") {
		return "basic"
	}
	return "header"
}

func printServerTable(out io.Writer, rows []serverListRow) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tTYPE\tAUTH\tSTATE\tURL / COMMAND"); err != nil {
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
	return printServerErrors(out, rows)
}

func printListedServer(out io.Writer, row serverListRow) error {
	kind := serverTransport(row.Config)
	if row.State == "invalid" {
		kind = unknownTransport
	}
	_, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n",
		row.Config.Name, kind, row.Auth, row.State, serverTarget(row.Config))
	return err
}

func printServerErrors(out io.Writer, rows []serverListRow) error {
	for _, row := range rows {
		if row.Err == nil {
			continue
		}
		detail := serverListError(row)
		if _, err := fmt.Fprintf(out, "  %s: %s\n", row.Config.Name, detail); err != nil {
			return err
		}
	}
	return nil
}

func serverListError(row serverListRow) string {
	if row.State == "needs-auth" {
		if row.Auth == "oauth" {
			return fmt.Sprintf("run `mini auth %s`", row.Config.Name)
		}
		return "authentication required; check configured credentials"
	}
	if errors.Is(row.Err, context.DeadlineExceeded) {
		return "connection timed out"
	}
	err := row.Err
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	return ansi.Truncate(singleLine(err), 100, "…")
}
