package initcmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

// Summary is what init prints when it ends: what is set up, what is left for the user, and
// what failed, each with the step that finishes it.
func Summary(r Report) string {
	var b strings.Builder
	writeServers(&b, r)
	writeIgnored(&b, r)
	writeUnusedEnvHeaders(&b, r)
	writeSkipped(&b, r.Skipped)
	writeFailures(&b, r)
	if r.Connected == nil {
		writeManualConnect(&b, r)
	} else {
		writeConnected(&b, r)
	}
	return b.String()
}

func writeServers(b *strings.Builder, r Report) {
	unfinished := slices.DeleteFunc(slices.Clone(r.Servers), func(s ServerStatus) bool { return s.Finish == Ready })
	switch {
	case len(r.Servers) == 0:
		fmt.Fprintln(b, "mini has no servers yet.")
	case len(unfinished) == 0:
		fmt.Fprintf(b, "mini is set up with %s.\n", plural(len(r.Servers), "server"))
	default:
		fmt.Fprintf(
			b,
			"mini is set up with %s, %d still %s finishing:\n",
			plural(len(r.Servers), "server"),
			len(unfinished),
			map[bool]string{true: "needs", false: "need"}[len(unfinished) == 1],
		)
	}
	width := nameWidth(unfinished)
	for _, s := range unfinished {
		fmt.Fprintf(b, "  %-*s  %s\n", width, s.Name, finishStep(r, s, width))
	}
	if len(r.AlreadyConfigured) > 0 {
		fmt.Fprintf(b, "Already configured in mini: %s\n", strings.Join(r.AlreadyConfigured, ", "))
	}
	if len(r.FromImport) > 0 {
		fmt.Fprintf(b, "Imported from your agents instead of the catalog: %s\n", strings.Join(r.FromImport, ", "))
	}
}

func finishStep(r Report, s ServerStatus, width int) string {
	file := config.ServerPath(r.ConfigDir, s.Name)
	more := "\n" + strings.Repeat(" ", width+4)
	switch s.Finish {
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
			s.SetupURL, auth.ResolvedCallbackURI(nil), file, more, more, more, more, r.miniCommand("auth", s.Name))
	}
	return "run: " + r.miniCommand("auth", s.Name)
}

// TokenEnvVar is the variable the token setup step tells the user to hold a server's token in.
func TokenEnvVar(serverName string) string {
	return strings.ToUpper(strings.ReplaceAll(serverName, "-", "_")) + "_TOKEN"
}

// Commands carry the config directory whenever agents are given one, so they act on the same servers.
func (r Report) miniCommand(args ...string) string {
	words := []string{"mini"}
	if i := slices.Index(r.Mini.Args, "--config"); i >= 0 && i+1 < len(r.Mini.Args) {
		words = append(words, "--config", shellQuote(r.Mini.Args[i+1]))
	}
	return strings.Join(append(words, args...), " ")
}

func writeSkipped(b *strings.Builder, skipped []SkippedServer) {
	if len(skipped) > 0 {
		fmt.Fprintln(b, "\nNot imported:")
	}
	for _, s := range skipped {
		fmt.Fprintf(b, "  %s\n", skippedLine(s))
	}
}

func skippedLine(s SkippedServer) string {
	switch s.Reason {
	case SkipEmptyName:
		return fmt.Sprintf("%q in %s: its name has no letters or digits mini can use", s.Name, s.Agent)
	case SkipSwitchedOff:
		return fmt.Sprintf("%s switched off in %s", s.Name, s.Agent)
	}
	return fmt.Sprintf("%s kept in %s: uses %s", s.Name, s.Agent, strings.Join(s.Refs, ", "))
}

func writeIgnored(b *strings.Builder, r Report) {
	for _, name := range slices.Sorted(maps.Keys(r.Ignored)) {
		fmt.Fprintf(b, "\n%s\n", IgnoredSettingsNote(name, r.Ignored[name], config.ServerPath(r.ConfigDir, name)))
	}
}

func writeUnusedEnvHeaders(b *strings.Builder, r Report) {
	for _, name := range slices.Sorted(maps.Keys(r.UnusedEnvHeaders)) {
		for _, note := range StaticHeaderNotes(name, r.UnusedEnvHeaders[name], config.ServerPath(r.ConfigDir, name)) {
			fmt.Fprintf(b, "\n%s\n", note)
		}
	}
}

// IgnoredSettingsNote and StaticHeaderNotes word the import caveats for init and mini add alike.
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
	for _, failure := range r.Sync.Failed {
		fmt.Fprintf(b, "\nCould not add %s: %v\n", failure.Name, failure.Err)
	}
	if r.StatusErr != nil {
		fmt.Fprintf(b, "\nCould not read mini's servers: %v\n", r.StatusErr)
	}
}

func nameWidth(statuses []ServerStatus) int {
	width := 0
	for _, s := range statuses {
		width = max(width, len(s.Name))
	}
	return width
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
