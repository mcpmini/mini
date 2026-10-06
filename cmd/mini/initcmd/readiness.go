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
	Name     string
	Finish   Finish
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
	if entry, ok := catalogEntryFor(sc, entries); ok {
		status.SetupURL = entry.SetupURL
		switch {
		case entry.Auth == catalog.AuthToken && !hasCredential(sc):
			status.Finish = NeedsToken
			return status
		case entry.Auth == catalog.AuthOAuth2App && (sc.Auth == nil || sc.Auth.ClientID == ""):
			status.Finish = NeedsOwnApp
			return status
		}
	}
	if sc.UsesOAuthLogin() && needsLogin(configDir, sc.Name) {
		status.Finish = NeedsLogin
	}
	return status
}

// A server is the catalog's when it runs the catalog's URL, under any name; one that only shares
// the name, like a local stdio github, isn't.
func catalogEntryFor(sc config.ServerConfig, entries []catalog.Entry) (catalog.Entry, bool) {
	i := slices.IndexFunc(entries, func(e catalog.Entry) bool {
		return sc.URL != "" && serverURLKey(e.URL) == serverURLKey(sc.URL)
	})
	if i < 0 {
		return catalog.Entry{}, false
	}
	return entries[i], true
}

// A refreshable token needs no login; ReadTokenState already counts it as usable.
func needsLogin(configDir, name string) bool {
	state, _ := auth.ReadTokenState(configDir, name) // nolint: errcheck // unreadable reads as needing a login
	return state.NeedsLogin()
}

func hasCredential(sc config.ServerConfig) bool {
	return len(sc.Headers) > 0 || (sc.Auth != nil && sc.Auth.Token != "")
}
