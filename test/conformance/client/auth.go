package main

import (
	"fmt"
	"os"

	"github.com/mcpmini/mini/internal/config"
)

// The suite's authorization servers approve every request, so following the redirects completes a login.
const loginBrowserCommand = "curl -sS -L -o /dev/null --max-time 20"

// basic-cimd passes only when the client_id is the suite's fixed client metadata document URL.
const suiteClientMetadataURL = "https://conformance-test.local/client-metadata.json"

func setUpAuth(sc scenario, mini miniCLI) error {
	cfg := config.DefaultConfig()
	cfg.BrowserCommand = loginBrowserCommand
	if err := mini.writeConfig(cfg); err != nil {
		return err
	}
	switch sc.Name {
	case "auth/pre-registration":
		return preRegister(sc, mini)
	case "auth/basic-cimd":
		return registerByMetadataDocument(sc, mini)
	}
	logLoginFailure("mini add", mini.add(sc.ServerURL))
	return nil
}

func registerByMetadataDocument(sc scenario, mini miniCLI) error {
	if err := mini.add(sc.ServerURL, "--no-connect").failure("mini add"); err != nil {
		return err
	}
	err := mini.updateServer(func(server *config.ServerConfig) {
		if server.Auth == nil {
			server.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
		}
		server.Auth.ClientID = suiteClientMetadataURL
		server.Auth.TokenEndpointAuthMethod = "none"
	})
	if err != nil {
		return err
	}
	logLoginFailure("mini auth", mini.auth())
	return nil
}

func preRegister(sc scenario, mini miniCLI) error {
	err := mini.createServer(config.ServerConfig{
		Name: serverName, Transport: config.TransportHTTP, URL: sc.ServerURL,
		Auth: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, ClientID: sc.Context.ClientID, ClientSecret: sc.Context.ClientSecret,
			Issuer: sc.Context.Issuer, TokenEndpointAuthMethod: "client_secret_basic",
		},
	})
	if err != nil {
		return fmt.Errorf("write pre-registered server: %w", err)
	}
	logLoginFailure("mini auth", mini.auth())
	return nil
}

// logLoginFailure doesn't fail the adapter: several auth scenarios expect mini to refuse the login, and the
// suite's checks judge whether it should have.
func logLoginFailure(operation string, result cliResult) {
	if err := result.failure(operation); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
