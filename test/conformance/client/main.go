// Command conformanceclient is the client command the official MCP conformance suite starts for each scenario.
// It connects mini to the scenario's server and uses it as an agent would, over stdio or through the CLI.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	sc, err := loadScenario()
	if err != nil {
		return err
	}
	bin := os.Getenv("MINIMCP_BIN")
	if bin == "" {
		return errors.New("MINIMCP_BIN is required")
	}
	configDir, err := os.MkdirTemp("", "mini-conformance-")
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.RemoveAll(configDir); removeErr != nil {
			fmt.Fprintln(os.Stderr, "remove config:", removeErr)
		}
	}()
	return runScenario(sc, miniCLI{bin: bin, configDir: configDir})
}

type scenario struct {
	Name      string
	Mode      mode
	ServerURL string
	Context   scenarioContext
}

type scenarioContext struct {
	ToolCalls    []toolCall `json:"toolCalls"`
	ClientID     string     `json:"client_id"`
	ClientSecret string     `json:"client_secret"`
	Issuer       string     `json:"issuer"`
}

func (sc scenario) isAuth() bool { return strings.HasPrefix(sc.Name, "auth/") }

func loadScenario() (scenario, error) {
	if len(os.Args) < 2 {
		return scenario{}, errors.New("missing conformance server URL")
	}
	m, err := parseMode(os.Getenv("MINI_CONFORMANCE_MODE"))
	if err != nil {
		return scenario{}, err
	}
	ctx, err := decodeContext(os.Getenv("MCP_CONFORMANCE_CONTEXT"))
	if err != nil {
		return scenario{}, err
	}
	return scenario{
		Name: os.Getenv("MCP_CONFORMANCE_SCENARIO"), Mode: m, ServerURL: os.Args[len(os.Args)-1], Context: ctx,
	}, nil
}

func decodeContext(raw string) (scenarioContext, error) {
	var ctx scenarioContext
	if raw == "" {
		return ctx, nil
	}
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return scenarioContext{}, fmt.Errorf("decode conformance context: %w", err)
	}
	return ctx, nil
}

func runScenario(sc scenario, mini miniCLI) error {
	if err := setUp(sc, mini); err != nil {
		return err
	}
	d, err := openDriver(sc, mini)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := d.close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "close mini:", closeErr)
		}
	}()
	if sc.Name == mrtrScenario {
		return runMRTR(d)
	}
	tools, err := d.tools()
	if err != nil {
		return err
	}
	return callAll(d, planFor(sc, tools))
}

func setUp(sc scenario, mini miniCLI) error {
	if sc.isAuth() {
		return setUpAuth(sc, mini)
	}
	return mini.add(sc.ServerURL, "--no-connect").failure("mini add")
}

func callAll(d driver, calls []toolCall) error {
	var failures []error
	for _, call := range calls {
		_, err := d.callTool(call)
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
