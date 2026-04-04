package server

import "github.com/mcpmini/mini/internal/config"

func (s *Server) RegisterAction(a config.ActionConfig) {
	s.reg.AddAction(expandActionEnv(a))
}

func (s *Server) LoadActions(configDir string) error {
	actions, err := config.LoadActions(configDir)
	if err != nil {
		return err
	}
	for _, a := range actions {
		s.reg.AddAction(expandActionEnv(a))
	}
	return nil
}

func expandActionEnv(a config.ActionConfig) config.ActionConfig {
	for k, v := range a.DefaultArgs {
		if sv, ok := v.(string); ok {
			a.DefaultArgs[k] = expandEnv(sv)
		}
	}
	return a
}
