package initcmd

import (
	"errors"
	"slices"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

//sumtype:decl
type Readiness interface {
	readiness()
}

type (
	ReadyState       struct{}
	NeedsLoginState  struct{}
	NeedsTokenState  struct{}
	NeedsOwnAppState struct{}
	NeedsEnvState    struct {
		Names []string
		Field string
	}
)

func (ReadyState) readiness()       {}
func (NeedsLoginState) readiness()  {}
func (NeedsTokenState) readiness()  {}
func (NeedsOwnAppState) readiness() {}
func (NeedsEnvState) readiness()    {}

// ServerStatus is one enabled server and what the user still has to do before it works.
type ServerStatus struct {
	Name      string
	Readiness Readiness
}

// Ready treats an absent readiness as the default ready state.
func (s ServerStatus) Ready() bool {
	if s.Readiness == nil {
		return true
	}
	_, ready := s.Readiness.(ReadyState)
	return ready
}

func (s ServerStatus) NeedsLogin() bool {
	_, needs := s.Readiness.(NeedsLoginState)
	return needs
}

func (s ServerStatus) NeedsOwnApp() bool {
	_, needs := s.Readiness.(NeedsOwnAppState)
	return needs
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
	status := ServerStatus{Name: sc.Name, Readiness: ReadyState{}}
	if sc.UnsetEnv != nil {
		status.Readiness = envReadiness(sc.UnsetEnv)
		return status
	}
	if entry, ok := catalogEntryFor(sc, entries); ok {
		switch {
		case entry.Auth == catalog.AuthToken && !hasCredential(sc):
			status.Readiness = NeedsTokenState{}
			return status
		case entry.Auth == catalog.AuthOAuth2App && !hasOwnApp(sc):
			status.Readiness = NeedsOwnAppState{}
			return status
		}
	}
	if sc.UsesOAuthLogin() && needsLogin(configDir, sc.Name) {
		status.Readiness = NeedsLoginState{}
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
	//nolint:errcheck // Unreadable token state conservatively requires login.
	state, _ := auth.ReadTokenState(configDir, name)
	return state.NeedsLogin()
}

func hasCredential(sc config.ServerConfig) bool {
	return !holdsPlaceholder(sc) && (len(sc.Headers) > 0 || (sc.Auth != nil && sc.Auth.Token != ""))
}

func hasOwnApp(sc config.ServerConfig) bool {
	return !holdsPlaceholder(sc) && sc.Auth != nil && sc.Auth.ClientID != ""
}

// Step says what the server still needs, naming only a login or a custom OAuth app; for anything
// else, env vars included, the step is the server's file, to keep init's output simple.
func (s ServerStatus) Step(file, login string) string {
	switch s.Readiness.(type) {
	case ReadyState, nil:
		return ""
	case NeedsLoginState:
		return "requires a login, run: " + login
	case NeedsOwnAppState:
		return "requires a custom OAuth app, edit " + file
	}
	return "requires manual configuration, edit " + file
}

func envReadiness(err error) NeedsEnvState {
	var unset *config.UnsetEnvError
	if errors.As(err, &unset) && unset != nil {
		return NeedsEnvState{Names: unset.Names, Field: unset.Field}
	}
	return NeedsEnvState{}
}
