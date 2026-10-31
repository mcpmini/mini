package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestRunListShowsConnectionStates(t *testing.T) {
	dir := t.TempDir()
	ready := fakeUnauthenticatedMCPServer(t)
	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(unauthorized.Close)
	var disabledRequests atomic.Int64
	disabled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		disabledRequests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(disabled.Close)
	for _, sc := range []config.ServerConfig{
		{Name: "ready", Transport: "http", URL: ready.URL},
		{Name: "login", Transport: "http", URL: unauthorized.URL},
		{Name: "disabled", Transport: "http", URL: disabled.URL, Enabled: new(false)},
		{Name: "failed", Transport: "http", URL: disabled.URL},
	} {
		configtest.WriteServer(t, dir, sc)
	}
	testutil.WriteFile(t, filepath.Join(dir, "servers", "broken.yaml"), "transport: [broken")
	var out bytes.Buffer
	if err := runList(dir, nil, &out); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"ready http ready", "login http needs-auth", "disabled http disabled", "failed http failed", "broken - invalid"} {
		if !strings.Contains(got, want) {
			t.Errorf("list missing %q: %s", want, out.String())
		}
	}
	if calls := disabledRequests.Load(); calls != 1 {
		t.Errorf("failed and disabled servers made %d requests, want only one from failed", calls)
	}
}

func TestProbeServerListDoesNotSerializeConnections(t *testing.T) {
	srv := buildStatusServer(config.DefaultConfig(), t.TempDir())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows := probeServerList(ctx, srv, config.Servers{Loaded: slowServerWaitingOnHealthyServer(t)})
	if rows[0].State != "failed" || rows[1].State != "ready" {
		t.Fatalf("states = %q, %q; want failed, ready", rows[0].State, rows[1].State)
	}
}

func TestPrintStatusTableDoesNotSerializeConnections(t *testing.T) {
	srv := buildStatusServer(config.DefaultConfig(), t.TempDir())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	failed, err := printStatusTable(statusTableParams{
		Context: ctx,
		Server:  srv,
		Out:     &out,
		Servers: config.Servers{Loaded: slowServerWaitingOnHealthyServer(t)},
	})
	if err != nil {
		t.Fatalf("printStatusTable: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !failed || len(lines) != 3 || !strings.HasPrefix(lines[1], "slow ") ||
		!strings.HasPrefix(strings.Join(strings.Fields(lines[2]), " "), "healthy http ok ") {
		t.Fatalf("failed = %v, status table:\n%s\nwant slow failed then healthy ok", failed, out.String())
	}
}

func slowServerWaitingOnHealthyServer(t *testing.T) []config.ServerConfig {
	t.Helper()
	ready := fakeUnauthenticatedMCPServer(t)
	readyStarted := make(chan struct{})
	var start sync.Once
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start.Do(func() { close(readyStarted) })
		ready.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(healthy.Close)
	releaseSlow := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-readyStarted:
		case <-releaseSlow:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(releaseSlow) })
	return []config.ServerConfig{
		{Name: "slow", Transport: "http", URL: slow.URL},
		{Name: "healthy", Transport: "http", URL: healthy.URL},
	}
}

func TestRunListShowsServerWithBrokenProjectionsAsReady(t *testing.T) {
	dir := t.TempDir()
	upstream := fakeUnauthenticatedMCPServer(t)
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "badproj", Transport: "http", URL: upstream.URL})
	configtest.WriteRawProjections(
		configtest.WriteRawProjectionsParams{T: t, Dir: dir, ServerName: "badproj", Projections: `"*": [bad]`},
	)
	var out bytes.Buffer
	if err := runList(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(got, "badproj http ready") {
		t.Errorf("server that still connects is not ready: %s", out.String())
	}
}
