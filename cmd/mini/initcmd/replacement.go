package initcmd

import (
	"maps"
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

type replacementRule struct {
	mini      miniEntryCheck
	miniToAdd agents.MiniEntry
	checks    map[string]error
}

func (s Setup) replacementRule(checks map[string]error) replacementRule {
	return replacementRule{
		mini:      miniEntryCheck{configDir: s.ConfigDir, selfPath: s.SelfPath},
		miniToAdd: MiniCommand(s.ConfigDir),
		checks:    checks,
	}
}

// A duplicate goes only when the agent ends up with a mini entry serving the servers checked here:
// its own, or the one init writes.
func (r replacementRule) servedAfterEdit(existing ExistingMini) bool {
	if existing != NoMiniEntry {
		return existing == MiniEntryServes
	}
	return r.mini.serves(
		agents.Server{Config: config.ServerConfig{Command: r.miniToAdd.Command, Args: r.miniToAdd.Args}},
	)
}

func (r replacementRule) splitDuplicates(existing ExistingMini, duplicates map[string]string) ([]string, []KeptEntry) {
	if !r.servedAfterEdit(existing) {
		return nil, keepAll(duplicates, errMiniInactive)
	}
	return r.splitByCheck(duplicates)
}

func (r replacementRule) mayRemove(existing ExistingMini, duplicates map[string]string) bool {
	return len(duplicates) > 0 && r.servedAfterEdit(existing)
}

func (r replacementRule) splitByCheck(duplicates map[string]string) ([]string, []KeptEntry) {
	var remove []string
	var kept []KeptEntry
	for _, entry := range slices.Sorted(maps.Keys(duplicates)) {
		err, checked := r.checks[duplicates[entry]]
		if !checked {
			err = errNotChecked
		}
		if err != nil {
			kept = append(kept, KeptEntry{Entry: entry, Server: duplicates[entry], Err: err})
		} else {
			remove = append(remove, entry)
		}
	}
	return remove, kept
}

func keepAll(duplicates map[string]string, reason error) []KeptEntry {
	var kept []KeptEntry
	for _, entry := range slices.Sorted(maps.Keys(duplicates)) {
		kept = append(kept, KeptEntry{Entry: entry, Server: duplicates[entry], Err: reason})
	}
	return kept
}
