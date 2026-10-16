package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// projectionFields has ProjectionConfig's fields without its decode methods, so they can decode it.
type projectionFields ProjectionConfig

var projectionOptions = projectionOptionNames()

func projectionOptionNames() []string {
	t := reflect.TypeFor[projectionFields]()
	names := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// UnmarshalYAML rejects unknown options, so a typo such as "exlude" fails instead of silently doing nothing.
func (p *ProjectionConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		keys := make([]string, 0, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			keys = append(keys, node.Content[i].Value)
		}
		if err := rejectUnknownProjectionOptions(keys); err != nil {
			return err
		}
	}
	return node.Decode((*projectionFields)(p))
}

// UnmarshalJSON rejects unknown options, as UnmarshalYAML does.
func (p *ProjectionConfig) UnmarshalJSON(data []byte) error {
	var options map[string]json.RawMessage
	if err := json.Unmarshal(data, &options); err != nil {
		return err
	}
	if err := rejectUnknownProjectionOptions(slices.Collect(maps.Keys(options))); err != nil {
		return err
	}
	return json.Unmarshal(data, (*projectionFields)(p))
}

func rejectUnknownProjectionOptions(keys []string) error {
	slices.Sort(keys)
	for _, key := range keys {
		if !slices.Contains(projectionOptions, key) {
			return fmt.Errorf(
				"unknown projection option %q; options are %s",
				key,
				strings.Join(projectionOptions, ", "),
			)
		}
	}
	return nil
}
