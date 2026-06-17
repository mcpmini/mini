package registry

import "github.com/mcpmini/mini/internal/config"

// AliasResolution is the result of resolving alias assignments for a tool set.
type AliasResolution struct {
	visible map[string]string // realToolName → visibleName
	dropped map[string]bool   // realToolNames whose alias was reverted due to collision
}

// AliasFor returns the accepted alias for realName, or "" if none was assigned
// or if the alias was reverted due to a collision.
func (r AliasResolution) AliasFor(realName string) string {
	if visibleName := r.visible[realName]; visibleName != realName {
		return visibleName
	}
	return ""
}

// WasDropped reports whether realName's alias was reverted due to a collision
// with a real tool name or with another alias.
func (r AliasResolution) WasDropped(realName string) bool {
	return r.dropped[realName]
}

// ResolveAliases computes the final visible name for each tool in realNames,
// reverting aliases that collide with a real tool name or with each other
// (symmetric — neither alias wins on a clash).
func ResolveAliases(realNames []string, aliasByToolName map[string]string) AliasResolution {
	visible, dropped, claim := buildAliasClaims(realNames, aliasByToolName)
	revertClashingAliases(visible, claim, dropped)
	return AliasResolution{visible: visible, dropped: dropped}
}

func buildAliasClaims(realNames []string, aliasByToolName map[string]string) (map[string]string, map[string]bool, map[string][]string) {
	nameSet := make(map[string]bool, len(realNames))
	for _, n := range realNames {
		nameSet[n] = true
	}
	visible := make(map[string]string, len(realNames))
	dropped := make(map[string]bool)
	claim := make(map[string][]string)
	for _, name := range realNames {
		visibleName := name
		if alias := aliasByToolName[name]; alias != "" && config.ValidToolName.MatchString(alias) {
			if nameSet[alias] {
				// Alias collides with a real tool name — revert immediately.
				dropped[name] = true
			} else {
				visibleName = alias
			}
		}
		visible[name] = visibleName
		claim[visibleName] = append(claim[visibleName], name)
	}
	return visible, dropped, claim
}

func revertClashingAliases(visible map[string]string, claim map[string][]string, dropped map[string]bool) {
	for realName, visibleName := range visible {
		if visibleName != realName && len(claim[visibleName]) > 1 {
			visible[realName] = realName
			dropped[realName] = true
		}
	}
}
