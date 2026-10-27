package config

import (
	"slices"
	"strings"
)

func (p *PermissionsConfig) Hides(toolName string) bool {
	if p == nil {
		return false
	}
	return slices.ContainsFunc(p.Hidden, func(name string) bool {
		return strings.EqualFold(name, toolName)
	})
}
