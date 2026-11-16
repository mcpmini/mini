package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mcpmini/mini/internal/daemon"
)

var errUseLocalLogin = errors.New("no daemon login")

var errLoginInterrupted = errors.New("login interrupted")

var daemonLocatorFor = locateDaemonIn

type daemonLocator func() (socket, token string, err error)

// locateDaemonIn never starts a daemon: one started here would keep this terminal's environment
// for every agent, and outlive one-shot runs such as CI. Without one, the token saved to disk
// reaches whichever daemon starts next.
func locateDaemonIn(configDir string) daemonLocator {
	return func() (string, string, error) {
		if err := daemon.CheckSocketPath(configDir); err != nil {
			return "", "", err
		}
		if !daemon.Running(configDir) {
			return "", "", errUseLocalLogin
		}
		token, err := daemon.ReadToken(configDir)
		return daemon.SocketPath(configDir), token, err
	}
}

func startDaemonLogin(ctx context.Context, p pkceFlowParams) (browserLogin, error) {
	socket, token, err := p.locateDaemon()
	if err != nil {
		return browserLogin{}, errUseLocalLogin
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	stream, err := openDaemonLogin(ctx, daemonLoginRequest{socket: socket, token: token, server: p.serverName})
	if err != nil {
		cancel()
		return browserLogin{}, err
	}
	url, err := stream.readURL()
	if err != nil {
		stream.close()
		cancel()
		return browserLogin{}, err
	}
	p.opener(url) //nolint:errcheck // the caller shows the URL, so a failed open only costs a click
	wait := func() (loginResult, error) {
		defer cancel()
		defer stream.close()
		return stream.readOutcome()
	}
	return browserLogin{url: url, wait: wait}, nil
}

type daemonLoginRequest struct {
	socket, token, server string
}

func openDaemonLogin(ctx context.Context, r daemonLoginRequest) (*daemonLoginStream, error) {
	body, err := json.Marshal(daemon.LoginRequest{Server: r.server})
	if err != nil {
		return nil, err
	}
	loginURL := "http://localhost" + daemon.LoginPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	// No client timeout: ctx bounds the whole login, browser step included.
	resp, err := daemon.SocketClient(r.socket, 0).Do(req) //nolint:bodyclose // the returned stream owns and closes it
	if err != nil {
		return nil, loginInterrupted(ctx)
	}
	stream := &daemonLoginStream{ctx: ctx, body: resp.Body, lines: json.NewDecoder(resp.Body)}
	if err := loginRefused(resp.StatusCode, stream); err != nil {
		stream.close()
		return nil, err
	}
	return stream, nil
}

func loginRefused(status int, stream *daemonLoginStream) error {
	if status == http.StatusOK {
		return nil
	}
	var line daemon.LoginLine
	if err := stream.lines.Decode(&line); err != nil || line.Message == "" {
		return fmt.Errorf("login failed (HTTP %d)", status)
	}
	return errors.New(line.Message)
}

type daemonLoginStream struct {
	ctx   context.Context
	body  io.ReadCloser
	lines *json.Decoder
}

func (s *daemonLoginStream) close() {
	s.body.Close() //nolint:errcheck // the stream is fully read or abandoned; Close only releases the connection
}

func (s *daemonLoginStream) readURL() (string, error) {
	var line daemon.LoginLine
	if err := s.lines.Decode(&line); err != nil || line.URL == "" {
		return "", loginInterrupted(s.ctx)
	}
	return line.URL, nil
}

func (s *daemonLoginStream) readOutcome() (loginResult, error) {
	var line daemon.LoginLine
	if err := s.lines.Decode(&line); err != nil {
		return loginResult{}, loginInterrupted(s.ctx)
	}
	if line.Error != "" {
		return loginResult{}, errors.New(cmp.Or(line.Message, line.Error))
	}
	if !line.OK {
		return loginResult{}, loginInterrupted(s.ctx)
	}
	return loginResultFrom(line), nil
}

func loginResultFrom(line daemon.LoginLine) loginResult {
	var result loginResult
	if line.Expires != "" {
		//nolint:errcheck // an unparsable expiry reads as none, which is what the terminal then prints
		result.expiry, _ = time.Parse(time.RFC3339, line.Expires)
	}
	result.connectErr = line.ConnectError
	return result
}

// loginInterrupted keeps the context's error, so a login the caller cancelled reads as cancelled.
func loginInterrupted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", errLoginInterrupted, err)
	}
	return errLoginInterrupted
}
