package initcmd

import (
	"errors"

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
	URL       string
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

// CheckFindings is what a run's checks learned that no server file says.
type CheckFindings struct {
	// NoSelfRegistration names the servers whose authorization server lets no client register itself.
	NoSelfRegistration map[string]bool
	// NeedsCredential names the servers that refused the probe without an OAuth challenge.
	NeedsCredential map[string]bool
}

type ServerStatusParams struct {
	ConfigDir   string
	Findings    CheckFindings
	CatalogAuth map[string]catalog.AuthKind
}

// ServerStatuses judges every enabled server as mini would load it, with what the run's checks found.
func ServerStatuses(p ServerStatusParams) ([]ServerStatus, error) {
	servers, err := config.LoadServers(p.ConfigDir)
	if err != nil {
		return nil, err
	}
	var statuses []ServerStatus
	for _, sc := range servers.Loaded {
		if sc.IsEnabled() {
			statuses = append(statuses, serverStatus(sc, p))
		}
	}
	return statuses, nil
}

func serverStatus(sc config.ServerConfig, p ServerStatusParams) ServerStatus {
	status := ServerStatus{Name: sc.Name, URL: sc.URL, Readiness: ReadyState{}}
	switch {
	case sc.UnsetEnv != nil:
		status.Readiness = envReadiness(sc.UnsetEnv)
	case holdsTokenPlaceholder(sc), p.Findings.NeedsCredential[sc.Name]:
		status.Readiness = NeedsTokenState{}
	case holdsAppPlaceholder(sc):
		status.Readiness = NeedsOwnAppState{}
	case sc.UsesOAuthLogin() && needsLogin(p.ConfigDir, sc):
		status.Readiness = p.loginReadiness(sc)
	}
	return status
}

func (p ServerStatusParams) loginReadiness(sc config.ServerConfig) Readiness {
	if sc.Auth.ClientID != "" || !p.Findings.NoSelfRegistration[sc.Name] {
		return NeedsLoginState{}
	}
	if p.CatalogAuth[sc.Name] == catalog.AuthToken {
		return NeedsTokenState{}
	}
	return NeedsOwnAppState{}
}

// A refreshable token needs no login; ReadTokenState already counts it as usable.
func needsLogin(configDir string, sc config.ServerConfig) bool {
	//nolint:errcheck // Unreadable token state conservatively requires login.
	state, _ := auth.ReadTokenState(configDir, sc)
	return state.NeedsLogin()
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
	case NeedsTokenState, NeedsEnvState:
		return "requires manual configuration, edit " + file
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
