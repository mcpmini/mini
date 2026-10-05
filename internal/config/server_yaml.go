package config

import "gopkg.in/yaml.v3"

// serverFields drops ServerConfig's YAML methods, so decoding it inside them doesn't recurse.
type serverFields ServerConfig

// UnmarshalYAML decodes projections apart from the other fields, so a mistake in them costs the
// server only its projections: it is recorded in ProjectionsErr rather than returned. The library
// still resolves the projections key, so a block reached through a merge key (<<) is handled too.
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
