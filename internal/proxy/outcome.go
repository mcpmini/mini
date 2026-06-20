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
	outcomeOK             outcomeKind = iota
	outcomeTransportDown              // dial failed → safe to respawn and retry
	outcomeUnauthorized               // 401 rejected before processing → safe to refresh token and retry
	outcomeNotInitialized             // session lost → safe to reinit and retry
	outcomeOther                      // post-send or unclassifiable; write may have run → never retry
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

// Dial-phase errors prove the request never reached the daemon (no bytes sent); everything
// else is treated as potentially post-send (fail safe — never double-execute a write).
func classifyDoError(body []byte, err error) forwardOutcome {
	if isDialError(err) {
		return forwardOutcome{kind: outcomeTransportDown, resp: daemonErrorResponse(body, "daemon unreachable: "+err.Error())}
	}
	return forwardOutcome{kind: outcomeOther, resp: daemonErrorResponse(body, "daemon error: "+err.Error())}
}

func isDialError(err error) bool {
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
		// Request reached daemon; can't prove it didn't execute → outcomeOther (never retried).
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return forwardOutcome{kind: outcomeOther, resp: daemonErrorResponse(body, fmt.Sprintf("daemon returned HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(errBody)))}
	}
	out := readForwardResponse(resp)
	if isNotInitialized(out) {
		return forwardOutcome{kind: outcomeNotInitialized, resp: out}
	}
	return forwardOutcome{kind: outcomeOK, resp: out}
}

// daemonURL's host is a placeholder — conn.client dials the Unix socket regardless; "localhost" passes the loopback-Host check.
const daemonURL = "http://localhost/mcp"

func newDaemonRequest(conn daemonConn, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, daemonURL, bytes.NewReader(body)) //nolint:noctx // no context at proxy level; daemon enforces per-call timeouts
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
