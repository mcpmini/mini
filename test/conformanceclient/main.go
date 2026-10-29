package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	params, err := loadRunParams()
	if err != nil {
		return err
	}
	params.Config, err = os.MkdirTemp(os.TempDir(), "mini-conformance-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(params.Config); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, "remove config:", cleanupErr)
		}
	}()
	return runScenario(params)
}

type runParams struct {
	Binary   string
	Scenario string
	Context  scenarioContext
	Mode     string
	URL      string
	Config   string
}

func loadRunParams() (runParams, error) {
	if len(os.Args) < 2 {
		return runParams{}, fmt.Errorf("missing conformance server URL")
	}
	params := runParams{
		Binary: os.Getenv("MINIMCP_BIN"), Scenario: os.Getenv("MCP_CONFORMANCE_SCENARIO"),
		Mode: os.Getenv("MINI_CONFORMANCE_MODE"), URL: os.Args[len(os.Args)-1],
	}
	if params.Binary == "" {
		return runParams{}, fmt.Errorf("MINIMCP_BIN is required")
	}
	if params.Mode == "" {
		params.Mode = "agent"
	}
	if params.Mode != "agent" && params.Mode != "cli" {
		return runParams{}, fmt.Errorf("unsupported MINI_CONFORMANCE_MODE %q", params.Mode)
	}
	ctx, err := decodeContext(os.Getenv("MCP_CONFORMANCE_CONTEXT"))
	params.Context = ctx
	return params, err
}

func runScenario(params runParams) error {
	if err := setupMini(params); err != nil {
		return err
	}
	tools, peer, err := scenarioTools(params)
	if err != nil {
		return err
	}
	if peer != nil {
		defer closeAgent(peer)
	}
	if params.Scenario == "sep-2322-client-request-state" && params.Mode == "agent" {
		return runMRTR(peer)
	}
	return runPlan(params, tools, peer)
}

func setupMini(params runParams) error {
	if isAuthScenario(params.Scenario) {
		return runAuthSetup(authSetupParams{
			Binary: params.Binary, Config: params.Config, URL: params.URL,
			Scenario: params.Scenario, Context: os.Getenv("MCP_CONFORMANCE_CONTEXT"),
		})
	}
	result := runCLI(cliParams{
		Binary: params.Binary, Config: params.Config, Timeout: 60 * time.Second,
		Args: []string{"add", "conf", "--url", params.URL, "--no-connect"},
	})
	if result.err != nil {
		return fmt.Errorf("mini add: %w: %s", result.err, result.combined())
	}
	return nil
}

func scenarioTools(params runParams) ([]tool, *agentPeer, error) {
	if params.Mode == "cli" {
		tools, err := cliTools(params)
		return tools, nil, err
	}
	peer, err := startAgent(agentStartParams{Binary: params.Binary, Config: params.Config})
	if err != nil {
		return nil, nil, err
	}
	tools, err := peer.tools()
	if err != nil {
		closeAgent(peer)
		return nil, nil, err
	}
	return tools, peer, nil
}

func cliTools(params runParams) ([]tool, error) {
	list := runCLI(cliParams{
		Binary: params.Binary, Config: params.Config, Timeout: 60 * time.Second,
		Args: []string{"ls", "conf"},
	})
	if list.err != nil && isAuthScenario(params.Scenario) && isUnauthorized(list.combined()) {
		retryAuth(params)
		list = runCLI(cliParams{
			Binary: params.Binary, Config: params.Config, Timeout: 60 * time.Second,
			Args: []string{"ls", "conf"},
		})
	}
	return listOutput(list)
}

func retryAuth(params runParams) {
	result := runCLI(cliParams{
		Binary: params.Binary, Config: params.Config, Timeout: 60 * time.Second,
		Args: []string{"auth", "conf"},
	})
	if result.err != nil {
		fmt.Fprintln(os.Stderr, "mini auth retry:", result.err, result.combined())
	}
}

func runPlan(params runParams, tools []tool, peer *agentPeer) error {
	plan := selectPlan(planParams{
		Scenario: params.Scenario, Context: params.Context, Tools: tools, Agent: params.Mode == "agent",
	})
	var failures []error
	for _, call := range plan {
		failures = append(failures, executePlannedCall(params, peer, call))
	}
	return errors.Join(failures...)
}

func executePlannedCall(params runParams, peer *agentPeer, call plannedCall) error {
	if params.Mode == "agent" {
		_, err := peer.callTool(call.Name, call.Arguments, nil)
		return err
	}
	_, err := callCLI(cliCallParams{
		Binary: params.Binary, Config: params.Config, Name: call.Name, Arguments: call.Arguments,
	})
	return err
}

func closeAgent(peer *agentPeer) {
	if err := peer.close(); err != nil {
		fmt.Fprintln(os.Stderr, "close agent:", err)
	}
}

func isUnauthorized(output string) bool {
	return strings.Contains(output, "unauthorized (") ||
		strings.Contains(output, "status 401") || strings.Contains(output, "status 403")
}

type authSetupParams struct {
	Binary   string
	Config   string
	URL      string
	Scenario string
	Context  string
}

func runAuthSetup(params authSetupParams) error {
	if err := os.WriteFile(
		filepath.Join(params.Config, "config.yaml"),
		[]byte("browser_command: \"curl -sS -L -o /dev/null --max-time 20\"\n"), 0o600,
	); err != nil {
		return err
	}
	if params.Scenario == "auth/pre-registration" {
		return runPreRegistration(params)
	}
	runOAuthAdd(params)
	return nil
}

func runOAuthAdd(params authSetupParams) {
	result := runCLI(cliParams{
		Binary: params.Binary, Config: params.Config, Timeout: 60 * time.Second,
		Args: []string{"add", "conf", "--url", params.URL},
	})
	logNonFatalLoginFailure("mini add auth", result)
}

func runPreRegistration(params authSetupParams) error {
	var values map[string]any
	if err := json.Unmarshal([]byte(params.Context), &values); err != nil {
		return fmt.Errorf("decode auth context: %w", err)
	}
	if err := writePreRegistration(params.Config, params.URL, values); err != nil {
		return err
	}
	result := runCLI(cliParams{
		Binary: params.Binary, Config: params.Config, Timeout: 60 * time.Second,
		Args: []string{"auth", "conf"},
	})
	logNonFatalLoginFailure("mini auth", result)
	return nil
}

func writePreRegistration(config, url string, values map[string]any) error {
	serverDir := filepath.Join(config, "servers")
	if err := os.MkdirAll(serverDir, 0o700); err != nil {
		return err
	}
	clientID := yamlString(values["client_id"])
	clientSecret := yamlString(values["client_secret"])
	issuer := yamlString(values["issuer"])
	body := fmt.Sprintf(
		"name: conf\ntransport: http\nurl: %s\nauth:\n  type: oauth2\n  client_id: %s\n  client_secret: %s\n  issuer: %s\n  token_endpoint_auth_method: client_secret_basic\n",
		yamlString(url),
		clientID,
		clientSecret,
		issuer,
	)
	return os.WriteFile(filepath.Join(serverDir, "conf.yaml"), []byte(body), 0o600)
}

func yamlString(value any) string {
	text, _ := value.(string)
	return strconv.Quote(text)
}

func runMRTR(peer *agentPeer) error {
	if err := mrtrFlow(peer, "test_mrtr_echo_state", "test_mrtr_unrelated"); err != nil {
		return err
	}
	if err := mrtrFlow(peer, "test_mrtr_no_state", ""); err != nil {
		return err
	}
	_, err := peer.callTool("test_mrtr_no_result_type", map[string]any{}, nil)
	return err
}

const maxMRTRRounds = 4

func mrtrFlow(peer *agentPeer, name, interloper string) error {
	result, err := peer.callTool(name, map[string]any{}, nil)
	if err != nil {
		return err
	}
	for round := 0; result["resultType"] == "input_required"; round++ {
		if round == maxMRTRRounds {
			return fmt.Errorf("%s: still input_required after %d rounds", name, maxMRTRRounds)
		}
		if interloper != "" {
			if _, callErr := peer.callTool(interloper, map[string]any{}, nil); callErr != nil {
				return callErr
			}
			interloper = ""
		}
		extra, extraErr := nextRoundExtra(result)
		if extraErr != nil {
			return extraErr
		}
		result, err = peer.callTool(name, map[string]any{}, extra)
		if err != nil {
			return err
		}
	}
	return nil
}

func nextRoundExtra(previous map[string]any) (*transport.ToolCallMRTR, error) {
	responses, err := json.Marshal(inputResponses(previous))
	if err != nil {
		return nil, err
	}
	var state json.RawMessage
	if value, ok := previous["requestState"]; ok {
		state, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	return &transport.ToolCallMRTR{InputResponses: responses, RequestState: state}, nil
}

func logNonFatalLoginFailure(operation string, result cliResult) {
	if result.err != nil {
		fmt.Fprintln(os.Stderr, operation+":", result.err, result.combined())
	}
}
