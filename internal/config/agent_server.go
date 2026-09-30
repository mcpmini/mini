package config

import (
	"errors"

	"gopkg.in/yaml.v3"
)

func ValidateAgentServer(sc ServerConfig) error {
	data, err := yaml.Marshal(sc)
	if err != nil {
		return err
	}
	// Server files are env-interpolated on load, so a ${VAR} would expand to the user's secrets.
	if envVarRef.Match(data) {
		return errors.New("environment variable references (${...}) aren't allowed")
	}
	// A server file config load rejects would stop mini from starting.
	s, err := parseServerConfig("the server config", data)
	if err != nil {
		return err
	}
	return validateServerProjectionFormats(s.Name, s.Projections)
}
