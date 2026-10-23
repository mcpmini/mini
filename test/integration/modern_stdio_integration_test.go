//go:build integration

package integration_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

func TestIntegrationModernStdioInputRetryAndToolChanges(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	writeModernStdioServer(t, cfg)
	client := startModernStdioProxy(t, cfg)
	client.sendNotification(transport.NotificationInitialized, nil)

	assertModernToolsListKeepsSchema(t, client)
	assertModernInputRetryCompletes(t, client)
	assertModernToolListRefreshes(t, client)
}

func assertModernToolsListKeepsSchema(t *testing.T, client *mcpClient) {
	t.Helper()
	list := client.callModern(t, "tools/list", nil)
	if !strings.Contains(string(list), `"name":"svc__confirm"`) || !strings.Contains(string(list), `"outputSchema"`) {
		t.Fatalf("modern tools/list did not preserve upstream tool schema: %s", list)
	}
}

func assertModernInputRetryCompletes(t *testing.T, client *mcpClient) {
	t.Helper()
	first := modernProxyCall("svc__confirm", nil, nil)
	got := client.callModern(t, "tools/call", first)
	if !strings.Contains(string(got), `"resultType":"input_required"`) ||
		!strings.Contains(string(got), `"requestState":"opaque-9007199254740993"`) {
		t.Fatalf("input-required result = %s", got)
	}
	retry := modernProxyCall(
		"svc__confirm",
		json.RawMessage(`{"r1":{"action":"accept"}}`),
		json.RawMessage(`"opaque-9007199254740993"`),
	)
	completed := client.callModern(t, "tools/call", retry)
	if !strings.Contains(string(completed), `"resultType":"complete"`) ||
		!strings.Contains(string(completed), `"accepted":true`) {
		t.Fatalf("retry result = %s", completed)
	}
}

func assertModernToolListRefreshes(t *testing.T, client *mcpClient) {
	t.Helper()
	client.listenModernTools(t)
	client.execProxyTool("svc__update_tools", map[string]any{}, nil)
	client.waitForNotification(transport.NotificationToolsChanged, 5*time.Second)
	updated := client.callModern(t, "tools/list", nil)
	if !strings.Contains(string(updated), `"name":"svc__dynamic_tool"`) {
		t.Fatalf("upstream list did not refresh: %s", updated)
	}
}

func (c *mcpClient) listenModernTools(t *testing.T) {
	t.Helper()
	id := c.nextID.Add(1)
	line := transporttest.ModernRequest(id, transport.MethodSubscriptionsListen, transport.ListenParams{
		Notifications: transport.ListenNotifications{ToolsListChanged: true},
	})
	if _, err := c.stdin.Write(line); err != nil {
		t.Fatal(err)
	}
	c.waitForNotification(transport.NotificationSubscriptionsAcknowledged, 5*time.Second)
}

func startModernStdioProxy(t *testing.T, configDir string) *mcpClient {
	t.Helper()
	stdin, stdout := startMiniConnectStandalone(t, configDir)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	client := newMCPClient(t, stdin, scanner)
	client.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	waitForServersConnected(t, client, connectableServers(t, configDir, nil))
	return client
}

func startMiniConnectStandalone(t *testing.T, configDir string) (io.WriteCloser, io.ReadCloser) {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "connect", "--standalone", "--log-level", "error")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return stdin, stdout
}

func writeModernStdioServer(t *testing.T, configDir string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configtest.WriteServer(t, configDir, config.ServerConfig{
		Name:    "svc",
		Command: bin,
		Args:    []string{"-test.run=^TestIntegrationModernStdioHelper$", "--", "modern-stdio-helper"},
	})
}

func TestIntegrationModernStdioHelper(t *testing.T) {
	if !hasModernHelperArg("modern-stdio-helper") {
		return
	}
	helper := &modernStdioHelper{ttlOnly: hasModernHelperArg("ttl-only")}
	scanner := transport.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req transport.Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		helper.handle(req)
	}
}

type modernStdioHelper struct {
	writeMu     sync.Mutex
	listChanged bool
	ttlOnly     bool
	lists       int
	listenID    json.RawMessage
}

type modernHelperCall struct {
	Name           string          `json:"name"`
	InputResponses json.RawMessage `json:"inputResponses"`
	RequestState   json.RawMessage `json:"requestState"`
	Meta           struct {
		ClientCapabilities struct {
			Elicitation json.RawMessage `json:"elicitation"`
		} `json:"io.modelcontextprotocol/clientCapabilities"`
	} `json:"_meta"`
}

func (h *modernStdioHelper) handle(req transport.Request) {
	switch req.Method {
	case "server/discover":
		h.discover(req)
	case "tools/list":
		h.listTools(req)
	case "tools/call":
		h.callTool(req)
	case transport.MethodSubscriptionsListen:
		h.listen(req)
	}
}

func (h *modernStdioHelper) write(value any) {
	h.writeMu.Lock()
	err := json.NewEncoder(os.Stdout).Encode(value)
	h.writeMu.Unlock()
	if err != nil {
		os.Exit(2)
	}
}

func (h *modernStdioHelper) discover(req transport.Request) {
	h.write(modernHelperResponse(req.ID, transport.DiscoverResult{
		SupportedVersions: transport.ModernVersions,
		Capabilities:      transport.ServerCapabilities{Tools: &transport.ToolsCapability{ListChanged: !h.ttlOnly}},
	}))
}

func (h *modernStdioHelper) listTools(req transport.Request) {
	if h.ttlOnly {
		h.listTTLTools(req)
		return
	}
	tools := []transport.ToolDefinition{
		{
			Name:         "confirm",
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"accepted":{"type":"boolean"}}}`),
		},
		{Name: "update_tools"},
	}
	if h.listChanged {
		tools = append(tools, transport.ToolDefinition{Name: "dynamic_tool"})
	}
	h.write(modernHelperResponse(req.ID, transport.ToolsListResult{Tools: tools}))
}

func (h *modernStdioHelper) callTool(req transport.Request) {
	var call modernHelperCall
	if err := json.Unmarshal(req.Params, &call); err != nil {
		os.Exit(2)
	}
	switch call.Name {
	case "confirm":
		h.confirm(req, call)
	case "update_tools":
		h.updateTools(req)
	default:
		h.write(modernHelperResponse(req.ID, json.RawMessage(`{"content":[]}`)))
	}
}

func (h *modernStdioHelper) confirm(req transport.Request, call modernHelperCall) {
	if len(call.RequestState) == 0 {
		h.requestConfirmation(req)
		return
	}
	if string(call.RequestState) != `"opaque-9007199254740993"` ||
		string(call.InputResponses) != `{"r1":{"action":"accept"}}` ||
		len(call.Meta.ClientCapabilities.Elicitation) == 0 {
		h.reportLostRetryFields(req)
		return
	}
	h.write(
		modernHelperResponse(
			req.ID,
			json.RawMessage(`{"resultType":"complete","structuredContent":{"accepted":true}}`),
		),
	)
}

func (h *modernStdioHelper) reportLostRetryFields(req transport.Request) {
	h.write(
		modernHelperResponse(
			req.ID,
			json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"retry fields lost"}]}`),
		),
	)
}

func (h *modernStdioHelper) requestConfirmation(req transport.Request) {
	h.write(
		modernHelperResponse(
			req.ID,
			json.RawMessage(
				`{"resultType":"input_required","inputRequests":{"r1":{"method":"elicitation/create","params":{"message":"Proceed?","requestedSchema":{"type":"object"}}}},"requestState":"opaque-9007199254740993"}`,
			),
		),
	)
}

func (h *modernStdioHelper) updateTools(req transport.Request) {
	h.listChanged = true
	h.write(modernHelperResponse(req.ID, json.RawMessage(`{"content":[{"type":"text","text":"updated"}]}`)))
	if len(h.listenID) > 0 {
		h.write(transport.NewToolsListChangedNotification(h.listenID))
	}
}

func (h *modernStdioHelper) listen(req transport.Request) {
	h.listenID = json.RawMessage(fmt.Sprint(req.ID))
	h.write(transport.NewSubscriptionAck(h.listenID, transport.ListenNotifications{ToolsListChanged: true}))
}

func modernHelperResponse(id any, result any) transport.Response {
	return transport.Response{
		JsonRPC: transport.JsonRPCVersion,
		ID:      modernHelperJSON(id),
		Result:  modernHelperJSON(result),
	}
}

func modernHelperJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func hasModernHelperArg(want string) bool {
	for _, arg := range os.Args {
		if arg == want {
			return true
		}
	}
	return false
}

func (c *mcpClient) callModern(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	id := c.nextID.Add(1)
	ch := make(chan *mcpResult, 1)
	c.pending.Store(id, ch)
	line := transporttest.ModernRequest(id, method, params)
	if _, err := c.stdin.Write(line); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-ch:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.raw
	case <-c.done:
		t.Fatal("mini closed while waiting for modern request")
		return nil
	}
}

type modernInputRetry struct {
	Name           string          `json:"name"`
	Arguments      map[string]any  `json:"arguments"`
	Meta           map[string]any  `json:"_meta"`
	InputResponses json.RawMessage `json:"inputResponses,omitempty"`
	RequestState   json.RawMessage `json:"requestState,omitempty"`
}

func modernProxyCall(name string, responses, state json.RawMessage) modernInputRetry {
	return modernInputRetry{
		Name:      name,
		Arguments: map[string]any{},
		Meta: map[string]any{
			"io.modelcontextprotocol/clientCapabilities": map[string]any{
				"elicitation": map[string]any{"form": map[string]any{}},
			},
		},
		InputResponses: responses,
		RequestState:   state,
	}
}
