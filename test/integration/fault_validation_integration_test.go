//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

var unsupportedFaultCases = []struct {
	name string
	body string
}{
	{name: "missing type", body: `{}`},
	{name: "empty type", body: `{"type":""}`},
	{name: "unknown type", body: `{"type":"typo"}`},
	{name: "unimplemented intermittent type", body: `{"type":"intermittent"}`},
}

func TestIntegrationFakeMCPRejectsUnsupportedInitialFaults(t *testing.T) {
	for _, tc := range unsupportedFaultCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), fakemcpBin, "--initial-fault", tc.body)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "unsupported fault type") {
				t.Fatalf("initial fault error = %v, output = %q; want unsupported fault rejection", err, output)
			}
		})
	}
}

func TestIntegrationFakeMCPRejectsUnsupportedControlFaults(t *testing.T) {
	addr := readControlAddr(t, startFakeMCPProcess(t, t.TempDir()))
	for _, tc := range unsupportedFaultCases {
		t.Run(tc.name, func(t *testing.T) {
			response := requestFaultControl(
				t,
				faultControlRequest{addr: addr, method: http.MethodPut, path: "/fault", body: tc.body},
			)
			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("fault status = %d, want %d", response.StatusCode, http.StatusBadRequest)
			}
		})
	}
	response := requestFaultControl(t, faultControlRequest{addr: addr, method: http.MethodGet, path: "/faults"})
	var faults []json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&faults); err != nil {
		t.Fatal(err)
	}
	if len(faults) != 0 {
		t.Errorf("unsupported requests registered %d faults, want none", len(faults))
	}
}

type faultControlRequest struct {
	addr   string
	method string
	path   string
	body   string
}

func requestFaultControl(t *testing.T, p faultControlRequest) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(),
		p.method,
		"http://"+p.addr+p.path,
		strings.NewReader(p.body),
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestIntegrationFakeMCPDispatchEffects(t *testing.T) {
	cases := []struct {
		name   string
		fault  string
		output string
		exits  bool
	}{
		{name: "raw write", fault: `{"method":"initialize","type":"bad_json"}`, output: "GARBAGE_NOT_JSON\n"},
		{name: "connection drop", fault: `{"method":"initialize","type":"connection_drop"}`, exits: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), fakemcpBin, "--initial-fault", tc.fault)
			cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")
			output, err := cmd.Output()
			if (err != nil) != tc.exits {
				t.Fatalf("process error = %v, want exits=%v", err, tc.exits)
			}
			if string(output) != tc.output {
				t.Errorf("stdout = %q, want %q", output, tc.output)
			}
		})
	}
}

func TestIntegrationFakeMCPContentFaultPayloads(t *testing.T) {
	t.Run("tool error message", func(t *testing.T) {
		client := faultServer(t, faultClientParams{
			Fixtures: map[string]string{"get_item": `{}`},

			Fault:       map[string]any{"method": "tools/call", "type": "error_response", "message": "injected marker"},
			ToolTimeout: "",
		})
		got := client.execResult("svc", "get_item", nil)
		if !got.IsError || !strings.Contains(got.Text, "injected marker") {
			t.Fatalf("fault response = %+v, want injected tool error", got)
		}
	})
	t.Run("oversized response length", func(t *testing.T) {
		client := faultServer(t, faultClientParams{
			Fixtures: map[string]string{"get_item": `{}`},

			Fault:       map[string]any{"method": "tools/call", "type": "oversized_response", "size_bytes": 64},
			ToolTimeout: "",
		})
		got := client.execTool("svc", "get_item", nil)
		if !strings.Contains(got, strings.Repeat("x", 64)) {
			t.Fatalf("response = %q, want 64-byte fault payload", got)
		}
	})
}
