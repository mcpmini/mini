//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/transport"
)

func TestIntegrationToolListTTLRefreshesWithoutNotification(t *testing.T) {
	for _, mode := range []string{"standalone", "daemon"} {
		t.Run(mode, func(t *testing.T) {
			cfg := shortConfigDir(t)
			bin, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			configtest.WriteServer(
				t,
				cfg,
				config.ServerConfig{
					Name:    "svc",
					Command: bin,
					Args: []string{
						"-test.run=^TestIntegrationModernStdioHelper$",
						"--",
						"modern-stdio-helper",
						"ttl-only",
					},
				},
			)
			client := startModernClientWithoutInitialize(t, cfg, mode)
			// A slow start can already show the refetched list. The helper never sends list_changed, so only TTL expiry refetches.
			if first := client.callModern(t, "tools/list", nil); !strings.Contains(string(first), `"name":"svc__`) {
				t.Fatalf("initial tool list %s", first)
			}
			timer := time.NewTimer(2100 * time.Millisecond)
			defer timer.Stop()
			<-timer.C
			refreshed := client.callModern(t, "tools/list", nil)
			if !strings.Contains(string(refreshed), `"name":"svc__new"`) ||
				strings.Contains(string(refreshed), `"name":"svc__old"`) {
				t.Fatalf("expired tool list %s", refreshed)
			}
		})
	}
}

func (h *modernStdioHelper) listTTLTools(req transport.Request) {
	h.lists++
	name := "old"
	if h.lists > 1 {
		name = "new"
	}
	h.write(
		modernHelperResponse(
			req.ID,
			transport.ToolsListResult{Tools: []transport.ToolDefinition{{Name: name}}, TTLMs: json.RawMessage(`2000`)},
		),
	)
}
