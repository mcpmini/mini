//go:build test

package auth_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestBeginLogin_completesWithoutWritingToTerminal(t *testing.T) {
	auth.UseEphemeralCallbackPort()
	mock := authtest.NewTokenServer(t)
	sc := &config.ServerConfig{Name: "synthetic", URL: mock.Srv.URL + "/mcp", Auth: mock.AuthConfig()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var token string
	output := captureTerminalOutput(t, func() {
		login, err := auth.BeginLogin(ctx, sc, beginLoginParams(t))
		if err != nil {
			t.Fatalf("BeginLogin: %v", err)
		}
		defer login.Close() //nolint:errcheck // Close always returns nil
		authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
		got, err := login.Wait(ctx)
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
		token = got.AccessToken
	})

	if token != "test-access-token" {
		t.Errorf("access token = %q, want test-access-token", token)
	}
	if output != "" {
		t.Errorf("login wrote to the terminal: %q", output)
	}
}

func TestBeginLogin_discoveryFailureReleasesCallbackPort(t *testing.T) {
	addr := freeLoopbackAddr(t)
	auth.UseCallbackListenAddr(addr)
	t.Cleanup(auth.UseEphemeralCallbackPort)
	noDiscovery := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(noDiscovery.Close)
	sc := &config.ServerConfig{
		Name: "synthetic",
		URL:  noDiscovery.URL + "/mcp",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2},
	}

	login, err := auth.BeginLogin(context.Background(), sc, beginLoginParams(t))

	if err == nil {
		login.Close() //nolint:errcheck // Close always returns nil
		t.Fatal("BeginLogin succeeded without discoverable endpoints")
	}
	if !strings.Contains(err.Error(), "resolve oauth endpoints") {
		t.Errorf("error = %v, want a resolve failure", err)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("callback port still bound after failed BeginLogin: %v", err)
	}
	listener.Close() //nolint:errcheck // test listener only proves the port is free
}

func TestBrowserLogin_callbackServerFailureEndsWait(t *testing.T) {
	listener := newFailingListener(t)
	close(listener.fail)

	var waitErr error
	output := captureTerminalOutput(t, func() {
		login, err := auth.StartBrowserLogin(&config.AuthConfig{}, listener)
		if err != nil {
			t.Fatalf("StartBrowserLogin: %v", err)
		}
		defer login.Close() //nolint:errcheck // Close always returns nil
		waitErr = waitForLogin(t, login)
	})

	if waitErr == nil || !strings.Contains(waitErr.Error(), "oauth callback server: synthetic accept failure") {
		t.Errorf("Wait error = %v, want the callback server failure", waitErr)
	}
	if output != "" {
		t.Errorf("callback server failure wrote to the terminal: %q", output)
	}
}

func TestBrowserLogin_callbackThatArrivedBeforeServerFailureStillCompletes(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	listener := newFailingListener(t)
	login, err := auth.StartBrowserLogin(mock.AuthConfig(), listener)
	if err != nil {
		t.Fatalf("StartBrowserLogin: %v", err)
	}
	defer login.Close() //nolint:errcheck // Close always returns nil

	auth.BufferCallbackCode(login, "test-auth-code")
	close(listener.fail)

	if err := waitForLogin(t, login); err != nil {
		t.Errorf("Wait = %v, want the buffered code to complete the login", err)
	}
}

func beginLoginParams(t *testing.T) auth.BeginLoginParams {
	return auth.BeginLoginParams{ConfigDir: t.TempDir(), ServerName: "synthetic", Clock: clock.NewFake()}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close() //nolint:errcheck // only the port number was needed
	return addr
}

func captureTerminalOutput(t *testing.T, run func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, logOut := os.Stdout, os.Stderr, log.Writer()
	os.Stdout, os.Stderr = w, w
	log.SetOutput(w) // log keeps the writer it had at init, so swapping os.Stderr alone misses it
	var captured bytes.Buffer
	copied := make(chan struct{})
	go func() { io.Copy(&captured, r); close(copied) }() //nolint:errcheck // a short read only shrinks the capture
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		log.SetOutput(logOut)
	}()
	run()
	w.Close() //nolint:errcheck // closing the write end only ends the copy
	<-copied
	return captured.String()
}

type failingListener struct {
	net.Listener
	fail      chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func newFailingListener(t *testing.T) *failingListener {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return &failingListener{Listener: inner, fail: make(chan struct{}), closed: make(chan struct{})}
}

func (l *failingListener) Accept() (net.Conn, error) {
	select {
	case <-l.fail:
		return nil, errors.New("synthetic accept failure")
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *failingListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.Listener.Close()
}
