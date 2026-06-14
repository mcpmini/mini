package registry

import (
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type aliasResolution struct {
	visible map[string]string // realToolName → visibleName (alias if assigned, realToolName if not or collision)
	dropped map[string]bool   // realToolNames whose alias was dropped due to a name collision
}

func (res aliasResolution) aliasFor(realName string) string {
	if visibleName := res.visible[realName]; visibleName != realName {
		return visibleName
	}
	return ""
}

// resolveAliases computes the final visible name for each tool, reverting to
// the real name for aliases that collide with a real tool name or with another
// alias (symmetric: no "first one wins").
func resolveAliases(defs []transport.ToolDefinition, aliasByToolName map[string]string) aliasResolution {
	realNames := make(map[string]bool, len(defs))
	for _, def := range defs {
		realNames[def.Name] = true
	}

	visible := make(map[string]string, len(defs))
	dropped := make(map[string]bool)
	claim := make(map[string][]string)

	for _, def := range defs {
		visibleName := def.Name
		if toolAlias := aliasByToolName[def.Name]; toolAlias != "" && config.ValidToolName.MatchString(toolAlias) {
			if realNames[toolAlias] {
				// Alias collides with a real tool name — revert immediately.
				dropped[def.Name] = true
			} else {
				visibleName = toolAlias
			}
		}
		visible[def.Name] = visibleName
		claim[visibleName] = append(claim[visibleName], def.Name)
	}

	// Revert aliases that collide with each other (both claim the same visible name).
	for realName, visibleName := range visible {
		if visibleName != realName && len(claim[visibleName]) > 1 {
			visible[realName] = realName
			dropped[realName] = true
		}
	}

	return aliasResolution{visible: visible, dropped: dropped}
}
