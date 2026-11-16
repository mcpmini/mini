package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/mcpmini/mini/internal/daemon"
)

var errUseLocalLogin = errors.New("no daemon login")

var errLoginInterrupted = errors.New("login interrupted")

// daemonReacher is replaced in tests: under go test, starting the daemon would run the test binary.
var daemonReacher = reachDaemon

// A nil daemonReach logs in in this process.
type daemonReach func() (socket, token string, err error)

func reachDaemon(configDir string) daemonReach {
	return func() (string, string, error) {
		if err := daemon.CheckSocketPath(configDir); err != nil {
			return "", "", err
		}
		token, err := daemonReresolver(configDir, slog.New(slog.DiscardHandler))()
		return daemon.SocketPath(configDir), token, err
	}
}

func startDaemonLogin(ctx context.Context, p pkceFlowParams) (browserLogin, error) {
	socket, token, err := p.reachDaemon()
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
	body, err := json.Marshal(map[string]string{"server": r.server})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/login", bytes.NewReader(body))
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
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusNotFound: // a daemon from before the login route
		return errUseLocalLogin
	}
	var line daemonLoginLine
	if err := stream.lines.Decode(&line); err != nil || line.Message == "" {
		return fmt.Errorf("login failed (HTTP %d)", status)
	}
	return errors.New(line.Message)
}

type daemonLoginLine struct {
	URL          string  `json:"url"`
	OK           bool    `json:"ok"`
	Expires      *string `json:"expires"`
	ConnectError *string `json:"connect_error"`
	Error        string  `json:"error"`
	Message      string  `json:"message"`
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
	var line daemonLoginLine
	if err := s.lines.Decode(&line); err != nil || line.URL == "" {
		return "", loginInterrupted(s.ctx)
	}
	return line.URL, nil
}

func (s *daemonLoginStream) readOutcome() (loginResult, error) {
	var line daemonLoginLine
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

func loginResultFrom(line daemonLoginLine) loginResult {
	var result loginResult
	if line.Expires != nil {
		//nolint:errcheck // an unparsable expiry reads as none, which is what the terminal then prints
		result.expiry, _ = time.Parse(time.RFC3339, *line.Expires)
	}
	if line.ConnectError != nil {
		result.connectErr = *line.ConnectError
	}
	return result
}

// loginInterrupted keeps the context's error, so a login the caller cancelled reads as cancelled.
func loginInterrupted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", errLoginInterrupted, err)
	}
	return errLoginInterrupted
}
