package config

import (
	"slices"
	"strings"
)

func (p *PermissionsConfig) LevelFor(toolName string) PermissionLevel {
	if p == nil {
		return PermOpen
	}
	if listedTool(p.Hidden, toolName) {
		return PermHidden
	}
	if listedTool(p.Protected, toolName) {
		return PermProtected
	}
	return defaultPermissionLevel(p.Default)
}

func listedTool(names []string, toolName string) bool {
	return slices.ContainsFunc(names, func(name string) bool {
		return strings.EqualFold(name, toolName)
	})
}

func defaultPermissionLevel(level string) PermissionLevel {
	switch PermissionLevel(level) {
	case PermProtected:
		return PermProtected
	case PermHidden:
		return PermHidden
	default:
		return PermOpen
	}
}
