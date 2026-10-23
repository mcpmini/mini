//go:build integration

package integration_test

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestIntegrationModernRequestsRecoverWithoutInitialize(t *testing.T) {
	for _, mode := range []string{"standalone", "daemon"} {
		t.Run(mode, func(t *testing.T) {
			cfg := shortConfigDir(t)
			writeModernStdioServer(t, cfg)
			client := startModernClientWithoutInitialize(t, cfg, mode)
			assertMalformedCapabilitiesRejected(t, client)
			discovery := client.callModern(t, "server/discover", nil)
			var result transport.DiscoverResult
			if err := json.Unmarshal(discovery, &result); err != nil || result.ResultType != "complete" {
				t.Fatalf("discover after invalid request = %s, decode error = %v", discovery, err)
			}
			assertModernToolsListKeepsSchema(t, client)
			assertModernInputRetryCompletes(t, client)
			assertMalformedCapabilitiesRejected(t, client)
			client.callModern(t, "ping", nil)
		})
	}
}

func startModernClientWithoutInitialize(t *testing.T, cfg, mode string) *mcpClient {
	t.Helper()
	if mode == "daemon" {
		startDaemon(t, cfg)
		stdin, scanner := startProxyCmd(t, cfg)
		return newMCPClient(t, stdin, scanner)
	}
	stdin, stdout := startMiniConnectStandalone(t, cfg)
	return newMCPClient(t, stdin, bufio.NewScanner(stdout))
}

func assertMalformedCapabilitiesRejected(t *testing.T, client *mcpClient) {
	t.Helper()
	_, err := client.call("server/discover", map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientCapabilities": []any{},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "rpc error -32602:") {
		t.Fatalf("malformed capabilities error = %v, want -32602", err)
	}
}
