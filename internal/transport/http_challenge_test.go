//go:build test

package transport

import (
	"context"
	"net/http"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/testutil"
)

type observingAuthProvider struct {
	fakeAuthProvider
	observed []AuthorizationChallenge
}

func (p *observingAuthProvider) ObserveAuthorizationChallenge(c AuthorizationChallenge) {
	p.observed = append(p.observed, c)
}

func TestSendOneObservesScopeChallengeWithoutRetry(t *testing.T) {
	srv := testutil.Serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("WWW-Authenticate", `Basic realm="ignored"`)
		w.Header().Add("WWW-Authenticate", `Bearer error="insufficient_scope", scope="write"`)
		w.WriteHeader(http.StatusForbidden)
	})
	p := &observingAuthProvider{}
	conn, err := NewHTTPConnection(
		HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake(), AuthProvider: p, AuthHeaderName: "Authorization"},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	resp, err := conn.sendOneWithAuthRetry(
		context.Background(),
		srv.Client(),
		func(ctx context.Context) (*http.Request, string, error) {
			req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			return req, "", requestErr
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	want := `Basic realm="ignored", Bearer error="insufficient_scope", scope="write"`
	if len(p.observed) != 1 || p.observed[0].StatusCode != 403 || p.observed[0].WWWAuthenticate != want {
		t.Fatalf("observations = %#v", p.observed)
	}
	if p.refreshCount() != 0 {
		t.Fatal("scope challenge attempted token refresh")
	}
}
