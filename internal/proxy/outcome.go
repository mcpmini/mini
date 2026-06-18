package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
)

type outcomeKind int

const (
	// outcomeOK: a normal JSON-RPC response (possibly a not-initialized error, see below).
	outcomeOK outcomeKind = iota
	// outcomeTransportDown: client.Do failed before the request reached the daemon
	// (dial / connection refused). The call never executed → safe to respawn and retry.
	outcomeTransportDown
	// outcomeUnauthorized: HTTP 401. Rejected before processing → safe to refresh token and retry.
	outcomeUnauthorized
	// outcomeNotInitialized: daemon up but session lost → safe to reinit and retry.
	outcomeNotInitialized
	// outcomeOther: client.Do failed after the request was sent, or any error we cannot
	// prove is pre-execution. The non-idempotent write may already have run → never retry.
	outcomeOther
)

type forwardOutcome struct {
	kind outcomeKind
	resp []byte
}

func classifyForward(conn daemonConn, body []byte) forwardOutcome {
	req, err := newDaemonRequest(conn, body)
	if err != nil {
		return forwardOutcome{kind: outcomeOther, resp: daemonErrorResponse(body, "build request: "+err.Error())}
	}
	resp, err := conn.client.Do(req)
	if err != nil {
		return classifyDoError(body, err)
	}
	defer resp.Body.Close()
	return classifyResponse(resp, body)
}

// classifyDoError maps a client.Do failure. Only a dial-time connection refusal proves
// the request never reached the daemon; everything else is treated as potentially
// post-send (fail safe — never double-execute a non-idempotent write).
func classifyDoError(body []byte, err error) forwardOutcome {
	if isDialConnRefused(err) {
		return forwardOutcome{kind: outcomeTransportDown, resp: daemonErrorResponse(body, "daemon unreachable: "+err.Error())}
	}
	return forwardOutcome{kind: outcomeOther, resp: daemonErrorResponse(body, "daemon unreachable: "+err.Error())}
}

func isDialConnRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

func classifyResponse(resp *http.Response, body []byte) forwardOutcome {
	if resp.StatusCode == http.StatusUnauthorized {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return forwardOutcome{kind: outcomeUnauthorized, resp: daemonErrorResponse(body, "daemon unauthorized")}
	}
	if resp.StatusCode >= 400 {
		// Non-401 HTTP error: the request reached the daemon, so we can't prove it didn't
		// execute → outcomeOther (never retried). Return a clean JSON-RPC error, not the raw body.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return forwardOutcome{kind: outcomeOther, resp: daemonErrorResponse(body, fmt.Sprintf("daemon returned HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(errBody)))}
	}
	out := readForwardResponse(resp)
	if isNotInitialized(out) {
		return forwardOutcome{kind: outcomeNotInitialized, resp: out}
	}
	return forwardOutcome{kind: outcomeOK, resp: out}
}

func newDaemonRequest(conn daemonConn, body []byte) (*http.Request, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", conn.port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body)) //nolint:noctx // no context at proxy level; daemon enforces per-call timeouts
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", conn.sessionID)
	if conn.token != "" {
		req.Header.Set("Authorization", "Bearer "+conn.token)
	}
	return req, nil
}

func readForwardResponse(resp *http.Response) []byte {
	if resp.StatusCode == http.StatusAccepted {
		return nil // notification — no response expected
	}
	result, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return bytes.TrimSpace(result)
}
