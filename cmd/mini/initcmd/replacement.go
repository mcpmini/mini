package initcmd

import (
	"maps"
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

type replacementRule struct {
	entryCheck miniEntryCheck
	miniToAdd  agents.MiniEntry
	checks     map[string]error
}

func (s Setup) replacementRule(checks map[string]error) replacementRule {
	return replacementRule{
		entryCheck: miniEntryCheck{configDir: s.ConfigDir, selfPath: s.SelfPath},
		miniToAdd:  MiniCommand(s.ConfigDir),
		checks:     checks,
	}
}

// A duplicate goes only when the agent ends up with a mini entry serving the servers checked here:
// its own, or the one init writes.
func (r replacementRule) servedAfterEdit(existing ExistingMini) bool {
	if existing != NoMiniEntry {
		return existing == MiniEntryServes
	}
	return r.entryCheck.serves(
		agents.Server{Config: config.ServerConfig{Command: r.miniToAdd.Command, Args: r.miniToAdd.Args}},
	)
}

func (r replacementRule) removableDuplicates(existing ExistingMini, duplicates map[string]string) []string {
	if !r.servedAfterEdit(existing) {
		return nil
	}
	var removable []string
	for _, entry := range slices.Sorted(maps.Keys(duplicates)) {
		if err, checked := r.checks[duplicates[entry]]; checked && err == nil {
			removable = append(removable, entry)
		}
	}
	return removable
}

func (r replacementRule) mayRemove(existing ExistingMini, duplicates map[string]string) bool {
	return len(duplicates) > 0 && r.servedAfterEdit(existing)
}
