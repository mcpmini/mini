package ops

import (
	"context"
	"errors"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type DetectOAuthParams struct {
	ConfigDir string
	Server    config.ServerConfig
	ConnErr   error
}

// DetectOAuth reports whether ConnErr proves Server needs OAuth, recording the finding
// so later loads of the server config merge OAuth auth in.
func DetectOAuth(ctx context.Context, p DetectOAuthParams) (bool, error) {
	sc := p.Server
	if !eligibleForOAuthDetection(sc) {
		return false, nil
	}
	// Skip re-running the PRM probe and rewriting the marker on every
	// reconnect backoff cycle against a persistently-401 upstream.
	if config.IsOAuthDetected(p.ConfigDir, sc.Name) {
		return true, nil
	}
	var uerr *transport.UnauthorizedError
	if !errors.As(p.ConnErr, &uerr) || !auth.RequiresOAuth(ctx, sc.URL, uerr.WWWAuthenticate) {
		return false, nil
	}
	if err := config.MarkOAuthDetected(p.ConfigDir, sc.Name); err != nil {
		return false, err
	}
	return true, nil
}

func eligibleForOAuthDetection(sc config.ServerConfig) bool {
	// Before auth is configured any header may hold a credential under a custom name (e.g. X-Api-Key),
	// and an expired static key answers with the same 401 as OAuth.
	return !sc.AgentAdded && sc.Auth == nil && sc.IsHTTPTransport() && len(sc.Headers) == 0
}
