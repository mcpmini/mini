package main

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type daemonHTTPService interface {
	http.Handler
	sessionEvictor
	ReleaseStartupHolds()
}

func startDaemonHTTP(ctx context.Context, p DaemonHTTPParams) error {
	httpSrv := daemonHTTPServer(p.Srv)
	serviceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpSrv.Serve(p.Listener) }()
	evictionDone := make(chan struct{})
	go runDaemonSessionEviction(serviceCtx, p.Srv, evictionDone)
	select {
	case serveErr := <-serveDone:
		return stopAfterDaemonServe(daemonHTTPServeResultParams{
			Err: serveErr, Server: httpSrv, Cancel: cancel, EvictionDone: evictionDone,
		})
	case <-ctx.Done():
		p.Srv.ReleaseStartupHolds()
		//nolint:contextcheck // Graceful shutdown uses a fresh bounded context after service cancellation.
		return shutdownDaemonHTTP(daemonHTTPShutdownParams{
			Server: httpSrv, ServeDone: serveDone, Cancel: cancel, EvictionDone: evictionDone,
		})
	}
}

func runDaemonSessionEviction(ctx context.Context, srv daemonHTTPService, done chan<- struct{}) {
	defer close(done)
	srv.RunSessionEviction(ctx, 30*time.Minute)
}

type daemonHTTPServeResultParams struct {
	Err          error
	Server       *http.Server
	Cancel       context.CancelFunc
	EvictionDone <-chan struct{}
}

func stopAfterDaemonServe(p daemonHTTPServeResultParams) error {
	p.Cancel()
	if errors.Is(p.Err, http.ErrServerClosed) {
		<-p.EvictionDone
		return nil
	}
	closeErr := p.Server.Close()
	<-p.EvictionDone
	return errors.Join(p.Err, closeErr)
}

type daemonHTTPShutdownParams struct {
	Server       *http.Server
	ServeDone    <-chan error
	Cancel       context.CancelFunc
	EvictionDone <-chan struct{}
}

func shutdownDaemonHTTP(p daemonHTTPShutdownParams) error {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	//nolint:contextcheck // The service context is canceled; shutdown needs a fresh bounded context.
	shutdownErr := p.Server.Shutdown(shutdownCtx)
	var closeErr error
	if shutdownErr != nil {
		closeErr = p.Server.Close()
	}
	p.Cancel()
	serveErr := <-p.ServeDone
	<-p.EvictionDone
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr, closeErr)
}

func daemonHTTPServer(srv http.Handler) *http.Server {
	return &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: per-call deadlines are enforced by ToolTimeout.
		// A fixed WriteTimeout would silently truncate any tool configured
		// with tool_timeout longer than the cap.
		MaxHeaderBytes: 64 << 10,
	}
}
