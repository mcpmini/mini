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

func (r replacementRule) splitDuplicates(existing ExistingMini, duplicates map[string]string) ([]string, []KeptEntry) {
	if !r.servedAfterEdit(existing) {
		return split(duplicates, func(string) error { return errMiniInactive })
	}
	return split(duplicates, r.checkError)
}

func (r replacementRule) mayRemove(existing ExistingMini, duplicates map[string]string) bool {
	return len(duplicates) > 0 && r.servedAfterEdit(existing)
}

func (r replacementRule) checkError(server string) error {
	if err, checked := r.checks[server]; checked {
		return err
	}
	return errNotChecked
}

func split(duplicates map[string]string, reason func(server string) error) ([]string, []KeptEntry) {
	var remove []string
	var kept []KeptEntry
	for _, entry := range slices.Sorted(maps.Keys(duplicates)) {
		if err := reason(duplicates[entry]); err != nil {
			kept = append(kept, KeptEntry{Entry: entry, Server: duplicates[entry], Err: err})
		} else {
			remove = append(remove, entry)
		}
	}
	return remove, kept
}
