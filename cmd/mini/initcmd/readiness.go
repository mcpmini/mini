package initcmd

import (
	"errors"

	"github.com/mcpmini/mini/internal/auth"
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

// ServerStatuses judges every enabled server as mini would load it. noSelfRegistration names the
// servers whose login needs the user's own OAuth app, as the run's discovery found.
func ServerStatuses(configDir string, noSelfRegistration map[string]bool) ([]ServerStatus, error) {
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return nil, err
	}
	var statuses []ServerStatus
	for _, sc := range servers.Loaded {
		if sc.IsEnabled() {
			statuses = append(statuses, serverStatus(configDir, sc, noSelfRegistration))
		}
	}
	return statuses, nil
}

func serverStatus(configDir string, sc config.ServerConfig, noSelfRegistration map[string]bool) ServerStatus {
	status := ServerStatus{Name: sc.Name, Readiness: ReadyState{}}
	switch {
	case sc.UnsetEnv != nil:
		status.Readiness = envReadiness(sc.UnsetEnv)
	case holdsTokenPlaceholder(sc):
		status.Readiness = NeedsTokenState{}
	case holdsAppPlaceholder(sc):
		status.Readiness = NeedsOwnAppState{}
	case sc.UsesOAuthLogin() && needsLogin(configDir, sc.Name):
		status.Readiness = NeedsLoginState{}
		if sc.Auth.ClientID == "" && noSelfRegistration[sc.Name] {
			status.Readiness = NeedsOwnAppState{}
		}
	}
	return status
}

// A refreshable token needs no login; ReadTokenState already counts it as usable.
func needsLogin(configDir, name string) bool {
	//nolint:errcheck // Unreadable token state conservatively requires login.
	state, _ := auth.ReadTokenState(configDir, name)
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
