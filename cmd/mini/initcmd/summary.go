package initcmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

// Summary is what init prints when it ends: what is set up, what is left for the user, and
// what failed, each with the step that finishes it.
func Summary(r Report) string {
	var b strings.Builder
	writeServers(&b, r)
	writeDroppedSettings(&b, r)
	writeStaticHeaders(&b, r)
	writeSkipped(&b, r)
	writeFailures(&b, r)
	if r.Connected == nil {
		writeHandConnect(&b, r.Agents)
	} else {
		writeConnected(&b, r)
	}
	return b.String()
}

func writeServers(b *strings.Builder, r Report) {
	unfinished := slices.DeleteFunc(slices.Clone(r.Servers), func(s ServerStatus) bool { return s.Readiness == Ready })
	writeServersHeadline(b, r, len(unfinished))
	width := nameWidth(unfinished)
	for _, s := range unfinished {
		fmt.Fprintf(b, "  %-*s  %s\n", width, s.Name, finishStep(r.ConfigDir, r.Agents.Mini, s, width))
	}
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
}

func writeServersHeadline(b *strings.Builder, r Report, unfinished int) {
	switch {
	case len(r.Servers) == 0 && r.ReadServersErr != nil:
	case len(r.Servers) == 0:
		fmt.Fprintln(b, "mini has no servers yet.")
	case unfinished == 0:
		fmt.Fprintf(b, "mini is set up with %s.\n", Plural(len(r.Servers), "server"))
	default:
		verb := "need"
		if unfinished == 1 {
			verb = "needs"
		}
		fmt.Fprintf(
			b,
			"mini is set up with %s, %d still %s finishing:\n",
			Plural(len(r.Servers), "server"),
			unfinished,
			verb,
		)
	}
}

func finishStep(configDir string, mini agents.MiniEntry, s ServerStatus, width int) string {
	file := config.ServerPath(configDir, s.Name)
	more := "\n" + strings.Repeat(" ", width+4)
	switch s.Readiness {
	case NeedsToken:
		return fmt.Sprintf(
			"needs a token: create one at %s, then add to %s:%s  headers:%s    Authorization: Bearer ${%s}",
			s.SetupURL,
			file,
			more,
			more,
			TokenEnvVar(s.Name),
		)
	case NeedsEnv:
		return fmt.Sprintf("needs %s set where mini runs (used in %s), or edit %s",
			strings.Join(s.UnsetEnv.Names, ", "), s.UnsetEnv.Field, file)
	case NeedsOwnApp:
		return fmt.Sprintf("needs your own OAuth app: register one at %s with redirect URI %s, then add to %s:%s"+
			"  auth:%s    type: oauth2%s    client_id: <your app's client ID>%sand run: %s",
			s.SetupURL, auth.ResolvedCallbackURI(nil), file, more, more, more, more, miniCommand(mini, "auth", s.Name))
	case MayNeedLogin:
		return "wasn't checked for a login; if it asks for one, run: " + miniCommand(mini, "auth", s.Name)
	}
	return "run: " + miniCommand(mini, "auth", s.Name)
}

// SetupStep words a server's finishing step as the summary does.
func SetupStep(configDir string, s ServerStatus) string {
	return finishStep(configDir, MiniCommand(configDir), s, 0)
}

// TokenEnvVar is the variable the token setup step tells the user to hold a server's token in.
func TokenEnvVar(serverName string) string {
	return strings.ToUpper(strings.ReplaceAll(serverName, "-", "_")) + "_TOKEN"
}

// Commands carry the config directory whenever agents are given one, so they act on the same servers.
func miniCommand(mini agents.MiniEntry, args ...string) string {
	words := []string{"mini"}
	if i := slices.Index(mini.Args, "--config"); i >= 0 && i+1 < len(mini.Args) {
		words = append(words, "--config", shellQuote(mini.Args[i+1]))
	}
	return strings.Join(append(words, args...), " ")
}

func writeSkipped(b *strings.Builder, r Report) {
	if len(r.Import.Skipped) > 0 {
		fmt.Fprintln(b, "\nNot imported:")
	}
	for _, s := range r.Import.Skipped {
		fmt.Fprintf(b, "  %s\n", skippedLine(s, r.ConfigDir))
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

func writeDroppedSettings(b *strings.Builder, r Report) {
	for _, name := range slices.Sorted(maps.Keys(r.Import.DroppedSettings)) {
		fmt.Fprintf(
			b,
			"\n%s\n",
			IgnoredSettingsNote(name, r.Import.DroppedSettings[name], config.ServerPath(r.ConfigDir, name)),
		)
	}
}

func writeStaticHeaders(b *strings.Builder, r Report) {
	for _, name := range slices.Sorted(maps.Keys(r.Import.StaticHeaders)) {
		for _, note := range StaticHeaderNotes(name, r.Import.StaticHeaders[name], config.ServerPath(r.ConfigDir, name)) {
			fmt.Fprintf(b, "\n%s\n", note)
		}
	}
}

func IgnoredSettingsNote(name string, settings []string, file string) string {
	return fmt.Sprintf("%s was imported without its %s, which mini doesn't support yet; if it fails to start, edit %s",
		name, strings.Join(settings, ", "), file)
}

func StaticHeaderNotes(name string, unused map[string]string, file string) []string {
	var notes []string
	for _, header := range slices.Sorted(maps.Keys(unused)) {
		envVar := unused[header]
		notes = append(
			notes,
			fmt.Sprintf(
				"%s was imported with its static %s header, since %s wasn't set; to use %s instead, set %s: ${%s} in %s",
				name,
				header,
				envVar,
				envVar,
				header,
				envVar,
				file,
			),
		)
	}
	return notes
}

func writeFailures(b *strings.Builder, r Report) {
	for _, u := range r.Import.Unreadable {
		fmt.Fprintf(
			b,
			"\nCould not read %s's config (%s), so none of its servers were imported: %v\n",
			u.Agent,
			u.ConfigPath,
			u.Err,
		)
	}
	for _, failure := range r.WriteErrors {
		fmt.Fprintf(b, "\nCould not add %s: %v\n", failure.Name, failure.Err)
	}
	if r.ReadServersErr != nil {
		fmt.Fprintf(b, "\nCould not read mini's servers: %v\n", r.ReadServersErr)
	}
}

func nameWidth(statuses []ServerStatus) int {
	width := 0
	for _, s := range statuses {
		width = max(width, len(s.Name))
	}
	return width
}

// Plural counts a noun that takes a plain s: "1 server", "2 servers".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
