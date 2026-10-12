package initcmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/config"
)

// A run without screens never showed which servers still need a step or which entries it left in
// the agents, so the summary does.
func writeWhatNoScreenShowed(b *strings.Builder, r Report) {
	writeSteps(b, r)
	if len(r.AlreadyConfigured) > 0 {
		fmt.Fprintf(b, "Already configured in mini: %s\n", strings.Join(r.AlreadyConfigured, ", "))
	}
	if len(r.AddCoveredByImport) > 0 {
		fmt.Fprintf(
			b,
			"Imported from your agents instead of the catalog: %s\n",
			strings.Join(r.AddCoveredByImport, ", "),
		)
	}
	for _, s := range r.Import.Skipped {
		fmt.Fprintf(b, "Not imported: %s\n", skippedLine(s, r.ConfigDir))
	}
}

func writeSteps(b *strings.Builder, r Report) {
	unfinished := slices.DeleteFunc(slices.Clone(r.Servers), func(s ServerStatus) bool { return s.Readiness == Ready })
	if len(unfinished) == 0 {
		return
	}
	fmt.Fprintln(b, "These MCPs need one more step:")
	width := Widest(unfinished, func(s ServerStatus) string { return s.Name })
	for _, s := range unfinished {
		step := s.Step(config.ServerPath(r.ConfigDir, s.Name), miniCommand(r.Agents, "auth", s.Name))
		fmt.Fprintf(b, "  %-*s  %s\n", width, s.Name, step)
	}
}

func skippedLine(s SkippedServer, configDir string) string {
	switch s.Reason {
	case SkipEmptyName:
		return fmt.Sprintf("%q in %s: its name has no letters or digits mini can use", s.Name, s.Agent)
	case SkipSwitchedOff:
		return fmt.Sprintf("%s switched off in %s", s.Name, s.Agent)
	case SkipSecondConfig:
		return fmt.Sprintf("%s in %s: a different config under that name is imported instead", s.Name, s.Agent)
	case SkipNameInMini:
		name := NormalizeName(s.Name)
		return fmt.Sprintf(
			"%s in %s: mini already has a different %s; to use this one, edit %s",
			s.Name,
			s.Agent,
			name,
			config.ServerPath(configDir, name),
		)
	}
	return fmt.Sprintf("%s kept in %s: uses %s", s.Name, s.Agent, strings.Join(s.Refs, ", "))
}
