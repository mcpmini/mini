//go:build integration

package integration_test

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestIntegrationModernMalformedCapabilitiesReturnInvalidParamsStandaloneAndDaemon(t *testing.T) {
	request := []byte(
		`{"jsonrpc":"2.0","id":17,"method":"server/discover","params":{"_meta":` +
			`{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
			`"io.modelcontextprotocol/clientCapabilities":[]}}}` + "\n",
	)
	t.Run("standalone", func(t *testing.T) {
		stdin, stdout := startMiniConnectStandalone(t, shortConfigDir(t))
		writeProxyLine(t, stdin, request)
		assertInvalidParamsResponse(t, readIntegrationLine(t, stdout))
	})
	t.Run("daemon", func(t *testing.T) {
		cfg := shortConfigDir(t)
		startDaemon(t, cfg)
		stdin, scanner := startProxyCmd(t, cfg, "")
		writeProxyLine(t, stdin, request)
		assertInvalidParamsResponse(t, waitForProxyLine(t, scanProxyLines(scanner), `"code":-32602`))
	})
}

func readIntegrationLine(t *testing.T, reader io.Reader) string {
	t.Helper()
	scanner := bufio.NewScanner(reader)
	if !scanner.Scan() {
		t.Fatalf("read mini response: %v", scanner.Err())
	}
	return scanner.Text()
}

func assertInvalidParamsResponse(t *testing.T, line string) {
	t.Helper()
	var response transport.Response
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	if response.ID != float64(17) || response.Error == nil || response.Error.Code != transport.CodeInvalidParams {
		t.Fatalf("response = %s, want request 17 with -32602", line)
	}
}

func TestIntegrationModernRequestsRecoverWithoutInitialize(t *testing.T) {
	for _, mode := range []string{"standalone", "daemon"} {
		t.Run(mode, func(t *testing.T) {
			cfg := shortConfigDir(t)
			writeModernStdioServer(t, cfg)
			client := startUninitializedComplianceClient(t, cfg, mode)
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

func startUninitializedComplianceClient(t *testing.T, cfg, mode string) *mcpClient {
	t.Helper()
	if mode == "daemon" {
		startDaemon(t, cfg)
		stdin, scanner := startProxyCmd(t, cfg, "")
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
