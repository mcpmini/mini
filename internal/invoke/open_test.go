//go:build test

package invoke_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

func TestOpen_handshakeTimeout_boundsDialAndList(t *testing.T) {
	for _, held := range []string{"initialize", "tools/list"} {
		t.Run("held at "+held, func(t *testing.T) {
			upstream, reached := upstreamHolding(t, held)
			handshakeClock := clock.NewFake()
			opened := openInBackground(t.Context(), openParams(upstream, handshakeClock))
			testutil.WaitForReceive(t, reached, "Open to reach "+held)
			waitForHandshakeTimer(t, handshakeClock)

			handshakeClock.Advance(5 * time.Second)

			err := testutil.WaitForReceive(t, opened, "Open to give up at handshake_timeout")
			if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "handshake_timeout (5s)") {
				t.Errorf("Open = %v, want a handshake_timeout (5s) deadline", err)
			}
		})
	}
}

func TestOpen_aCallerThatGivesUp_isNotReportedAsAHandshakeTimeout(t *testing.T) {
	upstream, _ := upstreamHolding(t, "initialize")
	ctx, cancel := context.WithCancel(t.Context())
	handshakeClock := clock.NewFake()
	opened := openInBackground(ctx, openParams(upstream, handshakeClock))
	waitForHandshakeTimer(t, handshakeClock)

	cancel()

	err := testutil.WaitForReceive(t, opened, "Open to return once its caller gave up")
	if err == nil || strings.Contains(err.Error(), "handshake_timeout") {
		t.Errorf("Open = %v, want the caller's cancellation, not a handshake timeout", err)
	}
}

func TestOpen_listRefusedForAMissingLogin_returnsReauthRequiredAndRecordsIt(t *testing.T) {
	upstream := mcptest.NewHTTP(t, mcptest.Upstream{Authorize: func(mcptest.Request) bool { return false }})
	p := openParams(upstream, nil)
	p.ConfigDir = t.TempDir()

	_, _, err := invoke.Open(t.Context(), p)

	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("Open = %v, want it to wrap ErrReauthRequired", err)
	}
	if !config.IsOAuthDetected(p.ConfigDir, "svc") {
		t.Error("the OAuth requirement wasn't recorded")
	}
}

func TestProbe_countsOnlyTheToolsAnAgentSees(t *testing.T) {
	upstream := mcptest.NewHTTP(t, mcptest.Upstream{Tools: []mcptest.Tool{{Name: "shown"}, {Name: "hidden"}}})
	sc := httpServer(upstream)
	sc.Permissions = &config.PermissionsConfig{Hidden: []string{"hidden"}}

	n, err := invoke.Probe(
		t.Context(),
		invoke.ProbeParams{Config: config.DefaultConfig(), ConfigDir: t.TempDir(), Server: sc},
	)

	if err != nil || n != 1 {
		t.Errorf("Probe = %d, %v; want 1 visible tool", n, err)
	}
}

func waitForHandshakeTimer(t *testing.T, c *clock.Fake) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("Open never started its handshake_timeout: %v", err)
	}
}

func upstreamHolding(t *testing.T, method string) (*mcptest.Server, <-chan struct{}) {
	reached := make(chan struct{}, 1)
	upstream := mcptest.NewHTTP(t, mcptest.Upstream{
		Tools: []mcptest.Tool{{Name: "ping"}},
		Methods: map[string]mcptest.Method{method: func(mcptest.Request) mcptest.Reply {
			select {
			case reached <- struct{}{}:
			default:
			}
			<-t.Context().Done()
			return mcptest.Reply{}
		}},
	})
	return upstream, reached
}

func httpServer(upstream *mcptest.Server) config.ServerConfig {
	return config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL, HandshakeTimeout: "5s"}
}

func openParams(upstream *mcptest.Server, handshakeClock clock.Clock) invoke.OpenParams {
	return invoke.OpenParams{
		DialParams:     invoke.DialParams{Config: config.DefaultConfig(), Server: httpServer(upstream)},
		HandshakeClock: handshakeClock,
	}
}

func openInBackground(ctx context.Context, p invoke.OpenParams) <-chan error {
	return testutil.RunInBackground(func() error {
		conn, _, err := invoke.Open(ctx, p)
		if conn != nil {
			conn.Close() //nolint:errcheck // only the error is under test
		}
		return err
	})
}
