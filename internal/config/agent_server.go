package config

import (
	"errors"

	"gopkg.in/yaml.v3"
)

// ValidateAgentServer rejects a server an agent asked to save unless its file
// would load back unchanged: a ${VAR} would expand to the user's secrets on the
// next start, and a field config load refuses would stop mini from starting.
func ValidateAgentServer(sc ServerConfig) error {
	data, err := yaml.Marshal(sc)
	if err != nil {
		return err
	}
	if envVarRef.Match(data) {
		return errors.New("environment variable references (${...}) aren't allowed")
	}
	s, err := parseServerConfig("the server config", data)
	if err != nil {
		return err
	}
	return validateServerProjectionFormats(s.Name, s.Projections)
}
