package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"

	"gopkg.in/yaml.v3"
)

func decodeSingleServerDocument(data []byte) (yaml.Node, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&doc); err != nil {
		return doc, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return doc, fmt.Errorf("server config has multiple documents; pass session_only")
	} else if !errors.Is(err, io.EOF) {
		return doc, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return doc, fmt.Errorf("server config must contain one mapping document; pass session_only")
	}
	return doc, nil
}

func validateProjectionNode(edit *projectionEdit) error {
	projections := edit.projections
	if projections != nil && projections.Kind == yaml.AliasNode {
		return fmt.Errorf("projections cannot be an alias; pass session_only")
	}
	if !directRulesMatchEffective(projections, edit.before.Projections) {
		return fmt.Errorf("projections inherited through a merge key cannot be edited; pass session_only")
	}
	if projections != nil && projections.Kind != yaml.MappingNode && projections.Tag != "!!null" {
		return fmt.Errorf("projections must be a mapping; pass session_only")
	}
	return nil
}

func directRulesMatchEffective(projections *yaml.Node, effective map[string]*ProjectionConfig) bool {
	var direct map[string]*ProjectionConfig
	if projections != nil && projections.Kind == yaml.MappingNode {
		if projections.Decode(&direct) != nil {
			return false
		}
	}
	return reflect.DeepEqual(direct, effective)
}
