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

func (c *HTTPConnection) postWithAuthRetry(ctx context.Context, rpcReq Request) (json.RawMessage, error) {
	result, err := c.post(ctx, rpcReq)
	if c.authProvider == nil || !isUnauthorized(err) {
		return result.body, err
	}
	if _, refreshErr := c.authProvider.RefreshAuthorization(ctx, result.sentAuth); refreshErr != nil {
		return nil, refreshErr
	}
	result, err = c.post(ctx, rpcReq)
	if isUnauthorized(err) {
		return nil, ReauthorizationError(c.serverName, err)
	}
	return result.body, err
}

func isUnauthorized(err error) bool {
	var uerr *UnauthorizedError
	return errors.As(err, &uerr)
}

// ReauthorizationError wraps cause with the remedy users should run.
func ReauthorizationError(serverName string, cause error) error {
	return fmt.Errorf("%s requires re-authorization; run `mini auth %s`: %w", serverName, serverName, cause)
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
