package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const listChangedNotif = `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`

func sseNotification(w http.ResponseWriter, payload string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
}

func testStreamConn(t *testing.T, srv *httptest.Server, out *bytes.Buffer, tokens *tokenSource) streamConn {
	t.Helper()
	return streamConn{
		client: srv.Client(), port: serverPort(t, srv), sessionID: "sess",
		tokens: tokens, out: out, mu: &sync.Mutex{},
	}
}

func TestStreamReadEvents_forwardsDataLines(t *testing.T) {
	var out bytes.Buffer
	sc := streamConn{out: &out, mu: &sync.Mutex{}}

	sc.readEvents(strings.NewReader("event: message\ndata: " + listChangedNotif + "\n\n"))

	got := out.String()
	if !strings.Contains(got, "notifications/tools/list_changed") {
		t.Errorf("expected the notification forwarded to stdout, got %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("forwarded notification must be newline-terminated, got %q", got)
	}
	if strings.Contains(got, "event:") {
		t.Errorf("only the data payload should be forwarded, got %q", got)
	}
}

func TestStreamReadEvents_skipsEmptyData(t *testing.T) {
	var out bytes.Buffer
	sc := streamConn{out: &out, mu: &sync.Mutex{}}

	sc.readEvents(strings.NewReader("data:\n\n: comment\n\n"))

	if out.Len() != 0 {
		t.Errorf("blank/comment lines must not be forwarded, got %q", out.String())
	}
}

func TestStreamConsumeOnce_forwardsServerNotifications(t *testing.T) {
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		sseNotification(w, listChangedNotif)
	})

	var out bytes.Buffer
	testStreamConn(t, srv, &out, &tokenSource{}).consumeOnce(context.Background())

	if !strings.Contains(out.String(), "notifications/tools/list_changed") {
		t.Errorf("consumeOnce should forward the stream's notification, got %q", out.String())
	}
}

func TestStreamConsumeOnce_unauthorizedRefreshesToken(t *testing.T) {
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})

	reloaded := false
	tokens := &tokenSource{reload: func() (string, error) { reloaded = true; return "new", nil }}
	var out bytes.Buffer
	testStreamConn(t, srv, &out, tokens).consumeOnce(context.Background())

	if !reloaded {
		t.Error("a 401 on the stream must trigger a token refresh so the next attempt re-auths")
	}
}

func TestStreamRun_stopsOnContextCancel(t *testing.T) {
	srv := startTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	var out bytes.Buffer
	sc := testStreamConn(t, srv, &out, &tokenSource{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sc.run(ctx); close(done) }()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return promptly after context cancellation")
	}
}
