package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

const streamRetryDelay = time.Second

type streamConn struct {
	client    *http.Client
	port      int
	sessionID string
	tokens    *tokenSource
	out       io.Writer
	mu        *sync.Mutex // shared with request forwarders; serializes stdout writes
}

func startNotificationStream(ctx context.Context, wg *sync.WaitGroup, sc streamConn) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc.run(ctx)
	}()
}

func (sc streamConn) run(ctx context.Context) {
	for ctx.Err() == nil {
		sc.consumeOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(streamRetryDelay):
		}
	}
}

func (sc streamConn) consumeOnce(ctx context.Context) {
	resp, err := sc.open(ctx)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		sc.readEvents(resp.Body)
	case http.StatusUnauthorized:
		sc.tokens.refresh() // daemon restarted and rotated the token; next attempt re-auths
	}
}

func (sc streamConn) open(ctx context.Context) (*http.Response, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", sc.port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sc.sessionID)
	if tok := sc.tokens.current(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return sc.client.Do(req)
}

func (sc streamConn) readEvents(body io.Reader) {
	scanner := transport.NewScanner(body)
	for scanner.Scan() {
		data, ok := bytes.CutPrefix(scanner.Bytes(), []byte("data:"))
		if !ok {
			continue
		}
		sc.writeNotification(bytes.TrimSpace(data))
	}
}

func (sc streamConn) writeNotification(data []byte) {
	if len(data) == 0 {
		return
	}
	sc.mu.Lock()
	fmt.Fprintf(sc.out, "%s\n", data) //nolint:errcheck
	sc.mu.Unlock()
}
