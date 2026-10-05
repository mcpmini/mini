package config

import "gopkg.in/yaml.v3"

type serverFields ServerConfig

// UnmarshalYAML records a mistake in projections in ProjectionsErr instead of returning it, so it
// costs the server only its projections.
func (sc *ServerConfig) UnmarshalYAML(value *yaml.Node) error {
	var file struct {
		serverFields `yaml:",inline"`
		Projections  yaml.Node `yaml:"projections"`
	}
	if err := value.Decode(&file); err != nil {
		return err
	}
	*sc = ServerConfig(file.serverFields)
	if file.Projections.Kind == 0 {
		return nil
	}
	if err := file.Projections.Decode(&sc.Projections); err != nil {
		sc.Projections = nil
		sc.ProjectionsErr = &SourceError{Err: err}
	}
	return nil
}

func (sc ServerConfig) MarshalYAML() (any, error) {
	return struct {
		serverFields `yaml:",inline"`
		Projections  map[string]*ProjectionConfig `yaml:"projections,omitempty"`
	}{serverFields(sc), sc.Projections}, nil
}
