package main

import (
	"fmt"
	"os"

	"github.com/mcpmini/mini/internal/config"
)

// The suite's authorization servers approve every request, so following the redirects completes a login.
const loginBrowserCommand = "curl -sS -L -o /dev/null --max-time 20"

func setUpAuth(sc scenario, mini miniCLI) error {
	cfg := config.DefaultConfig()
	cfg.BrowserCommand = loginBrowserCommand
	if err := mini.writeConfig(cfg); err != nil {
		return err
	}
	if sc.Name == "auth/pre-registration" {
		return preRegister(sc, mini)
	}
	logLoginFailure("mini add", mini.add(sc.ServerURL))
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
