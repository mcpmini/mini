package transport

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/mcpmini/mini/internal/clock"
)

type ProbeAuthorizationParams struct {
	URL    string
	Client *http.Client
	Clock  clock.Clock
}

func ProbeAuthorization(ctx context.Context, p ProbeAuthorizationParams) error {
	if p.Client == nil {
		return fmt.Errorf("ProbeAuthorizationParams.Client is required")
	}
	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL: p.URL, Clock: p.Clock, Client: p.Client, DisableRetryOnRateLimit: true,
	})
	if err != nil {
		return err
	}
	_, probeErr := conn.ListTools(ctx)
	return errors.Join(probeErr, conn.Close())
}
