//go:build integration

package integration_test

import (
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/mcptest"
)

type fakeHTTPMCP struct {
	srv   *mcptest.Server
	calls atomic.Int64
}

func newFakeHTTPMCP(t *testing.T, onCall func(int) (int, []byte)) *fakeHTTPMCP {
	t.Helper()
	f := &fakeHTTPMCP{}
	f.srv = mcptest.NewHTTP(t, mcptest.Upstream{
		Tools:   []mcptest.Tool{{Name: "get_item", Description: "test tool"}},
		Methods: map[string]mcptest.Method{"tools/call": f.toolsCall(onCall)},
	})
	return f
}

func (f *fakeHTTPMCP) toolsCall(onCall func(int) (int, []byte)) mcptest.Method {
	return func(mcptest.Request) mcptest.Reply {
		n := int(f.calls.Add(1))
		if onCall != nil {
			if status, body := onCall(n); status != 0 {
				return mcptest.Reply{Status: status, Body: body}
			}
		}
		return mcptest.Reply{
			Result: map[string]any{"content": []map[string]any{{"type": "text", "text": `{"ok":true}`}}},
		}
	}
}

type httpServerParams struct {
	ServerName string
	URL        string
}

func writeHTTPServer(t *testing.T, configDir string, p httpServerParams) {
	t.Helper()
	configtest.WriteServer(t, configDir, config.ServerConfig{Name: p.ServerName, Transport: "sse", URL: p.URL})
}

func httpServer(t *testing.T, onCall func(int) (int, []byte)) (*fakeHTTPMCP, *mcpClient) {
	t.Helper()
	f := newFakeHTTPMCP(t, onCall)
	cfg := t.TempDir()
	writeHTTPServer(t, cfg, httpServerParams{ServerName: "svc", URL: f.srv.URL})
	return f, startServer(t, cfg)
}

// envelopeOK returns true when call succeeds and the envelope ok field is true.
type envelopeCallParams struct {
	Client *mcpClient
	Server string
	Tool   string
}

func envelopeOK(t *testing.T, p envelopeCallParams) bool {
	t.Helper()
	_, isErr := p.Client.execToolAllowError(p.Server, p.Tool, nil)
	return !isErr
}
