package initcmd

import (
	"errors"
	"slices"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

type Finish int

const (
	Ready Finish = iota
	NeedsLogin
	NeedsToken
	NeedsOwnApp
	NeedsEnv
)

// ServerStatus is one enabled server and what the user still has to do before it works.
type ServerStatus struct {
	Name   string
	Finish Finish
	// SetupURL is where to get the token or register the app, from the catalog.
	SetupURL string
	UnsetEnv *config.UnsetEnvError
}

// ServerStatuses judges every enabled server as mini would load it. Catalog entries say which
// servers need a token or the user's own OAuth app; nothing in the server file can.
func ServerStatuses(configDir string, entries []catalog.Entry) ([]ServerStatus, error) {
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return nil, err
	}
	var statuses []ServerStatus
	for _, sc := range servers.Loaded {
		if sc.IsEnabled() {
			statuses = append(statuses, serverStatus(configDir, sc, entries))
		}
	}
	return statuses, nil
}

func serverStatus(configDir string, sc config.ServerConfig, entries []catalog.Entry) ServerStatus {
	status := ServerStatus{Name: sc.Name}
	if errors.As(sc.UnsetEnv, &status.UnsetEnv) {
		status.Finish = NeedsEnv
		return status
	}
	// A server that only shares the catalog's name, like a local stdio github, isn't the catalog's.
	if i := slices.IndexFunc(
		entries,
		func(e catalog.Entry) bool { return e.Name == sc.Name && e.URL == sc.URL },
	); i >= 0 {
		status.SetupURL = entries[i].SetupURL
		switch {
		case entries[i].Auth == catalog.AuthToken && !hasCredential(sc):
			status.Finish = NeedsToken
			return status
		case entries[i].Auth == catalog.AuthOAuth2App && (sc.Auth == nil || sc.Auth.ClientID == ""):
			status.Finish = NeedsOwnApp
			return status
		}
	}
	// A refreshable token needs no login; ReadTokenState already counts it as usable.
	if state, _ := auth.ReadTokenState(
		configDir,
		sc.Name,
	); sc.UsesOAuthLogin() &&
		state.NeedsLogin() { //nolint:errcheck // an unreadable token reads as needing a login, which is what to tell the user
		status.Finish = NeedsLogin
	}
	return status
}

func hasCredential(sc config.ServerConfig) bool {
	return len(sc.Headers) > 0 || (sc.Auth != nil && sc.Auth.Token != "")
}
