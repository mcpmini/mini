package initcmd

import (
	"context"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type discoverFunc func(ctx context.Context, serverURL string) (*auth.ServerMeta, error)

// A browser login needs a client ID: the user's own app, one registered earlier, or one the
// authorization server lets mini register itself.
func needsRegistrationCheck(configDir string, sc config.ServerConfig) bool {
	if !sc.UsesOAuthLogin() || sc.Auth.ClientID != "" || !needsLogin(configDir, sc.Name) {
		return false
	}
	_, err := auth.LoadRegistration(configDir, sc.Name)
	return auth.IsNotFound(err)
}

type registrationCheck struct {
	server   config.ServerConfig
	clock    clock.Clock
	discover discoverFunc
}

// selfRegisters follows the spec's discovery to the authorization server's metadata. A server that
// offers neither dynamic registration nor client ID metadata documents needs the user's own app;
// one whose discovery fails is still offered the login, whose error then says what went wrong.
func (c registrationCheck) selfRegisters(ctx context.Context) (selfRegisters, known bool) {
	ctx, cancel := clock.WithTimeout(ctx, c.clock, oauthCheckTimeout)
	defer cancel()
	meta, err := c.discover(ctx, c.server.URL)
	if err != nil {
		return false, false
	}
	return meta.RegistrationURL != "" || meta.CIMDSupported, true
}
