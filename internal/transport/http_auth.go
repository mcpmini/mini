package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// AuthorizationProvider supplies the auth header value, re-read on every request.
type AuthorizationProvider interface {
	Authorization(ctx context.Context) (string, error)
	// RefreshAuthorization refreshes only if the current value still equals stale.
	RefreshAuthorization(ctx context.Context, stale string) (string, error)
}

func (c *HTTPConnection) withAuthRetry(ctx context.Context, attempt func() (string, error)) error {
	sentAuth, err := attempt()
	if c.authProvider == nil || !isUnauthorized(err) {
		return err
	}
	if _, refreshErr := c.authProvider.RefreshAuthorization(ctx, sentAuth); refreshErr != nil {
		return refreshErr
	}
	_, err = attempt()
	if isUnauthorized(err) {
		return ReauthorizationError(c.serverName, err)
	}
	return err
}

func (c *HTTPConnection) postWithAuthRetry(ctx context.Context, rpcReq Request) (json.RawMessage, error) {
	var last postResult
	err := c.withAuthRetry(ctx, func() (string, error) {
		var err error
		last, err = c.post(ctx, rpcReq)
		return last.sentAuth, err
	})
	return last.body, err
}

func isUnauthorized(err error) bool {
	var uerr *UnauthorizedError
	return errors.As(err, &uerr)
}

// ErrReauthRequired marks failures that only `mini auth <server>` can fix.
var ErrReauthRequired = errors.New("re-authorization required")

// ReauthorizationError wraps cause with the remedy users should run.
func ReauthorizationError(serverName string, cause error) error {
	return fmt.Errorf("%s requires re-authorization; run `mini auth %s`: %w: %w", serverName, serverName, ErrReauthRequired, cause)
}

func (c *HTTPConnection) applyAuthProvider(ctx context.Context, req *http.Request) (string, error) {
	if c.authProvider == nil {
		return "", nil
	}
	value, err := c.authProvider.Authorization(ctx)
	if err != nil {
		return "", err
	}
	req.Header.Set(c.authHeaderName, value)
	return value, nil
}

func (c *HTTPConnection) sendOneWithAuthRetry(ctx context.Context, client *http.Client, build func(context.Context) (*http.Request, string, error)) (*http.Response, error) {
	var lastResp *http.Response
	err := c.withAuthRetry(ctx, func() (string, error) {
		req, sentAuth, err := build(ctx)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return sentAuth, err
		}
		if resp.StatusCode == http.StatusUnauthorized {
			wwwAuth := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			return sentAuth, &UnauthorizedError{WWWAuthenticate: wwwAuth}
		}
		lastResp = resp
		return sentAuth, nil
	})
	return lastResp, err
}
