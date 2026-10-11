package initcmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

// SummaryStyle marks the warning and the commands and paths a person acts on. A nil func leaves its text plain.
type SummaryStyle struct {
	Warning func(string) string
	Code    func(string) string
}

func (s SummaryStyle) warning(text string) string { return styled(s.Warning, text) }

func (s SummaryStyle) code(text string) string { return styled(s.Code, text) }

func styled(style func(string) string, text string) string {
	if style == nil {
		return text
	}
	return style(text)
}

// Summary is what init prints when it ends: what is set up, what is left for the user, and
// what failed, each with the step that finishes it.
func Summary(r Report, style SummaryStyle) string {
	var b strings.Builder
	writeServers(&b, r, style)
	writeDroppedSettings(&b, r)
	writeStaticHeaders(&b, r)
	writeSkipped(&b, r)
	writeFailures(&b, r)
	if r.Connected == nil {
		writeHandConnect(&b, r.Agents, style)
	} else {
		writeConnected(&b, r)
	}
	return b.String()
}

func writeServers(b *strings.Builder, r Report, style SummaryStyle) {
	writeServersHeadline(b, r)
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
	writeUnfinished(b, r, style)
}

func writeServersHeadline(b *strings.Builder, r Report) {
	switch {
	case len(r.Added) > 0:
		fmt.Fprintf(b, "Added %s to mini: %s\n", Plural(len(r.Added), "server"), strings.Join(r.Added, ", "))
	default:
		fmt.Fprintln(b, "No servers were added to mini.")
	}
}

func writeUnfinished(b *strings.Builder, r Report, style SummaryStyle) {
	unfinished := slices.DeleteFunc(slices.Clone(r.Servers), func(s ServerStatus) bool { return s.Readiness == Ready })
	if len(unfinished) == 0 {
		return
	}
	nameW := Widest(unfinished, func(s ServerStatus) string { return s.Name })
	requirementW := Widest(unfinished, ServerStatus.requirement)
	layout := stepLayout{column: strings.Repeat(" ", 2+nameW+2+requirementW+2), style: style}
	fmt.Fprintln(b, "\nThese servers need one more step before they work:")
	for _, s := range unfinished {
		fmt.Fprintf(b, "  %-*s  %-*s  %s\n", nameW, s.Name, requirementW, s.requirement(), r.finishStep(s, layout))
	}
}

func (s ServerStatus) requirement() string {
	switch s.Readiness {
	case NeedsToken:
		return "requires an access token"
	case NeedsOwnApp:
		return "requires an OAuth app"
	case NeedsEnv:
		return "requires " + JoinAnd(s.UnsetEnv.Names)
	}
	return "requires a login"
}

type stepLayout struct {
	column string
	style  SummaryStyle
}

func (r Report) finishStep(s ServerStatus, layout stepLayout) string {
	file := config.ServerPath(r.ConfigDir, s.Name)
	login := layout.style.code(miniCommand(r.Agents, "auth", s.Name))
	switch {
	case s.Readiness == NeedsEnv:
		return envStep(s.UnsetEnv, layout.style.code(file))
	case s.Readiness == NeedsLogin:
		return "run: " + login
	case s.StepsInFile && s.Readiness == NeedsOwnApp:
		return "finish the steps in " + layout.style.code(file) + ", then run: " + login
	case s.StepsInFile:
		return "finish the steps in " + layout.style.code(file)
	case s.Readiness == NeedsToken:
		return layout.addStep(file, catalog.AuthToken, s.SetupURL)
	}
	return layout.addStep(file, catalog.AuthOAuth2App, s.SetupURL) + "\n" + layout.column + "then run: " + login
}

// addStep is the credential step for a server whose file doesn't hold it.
func (l stepLayout) addStep(file, authKind, setupURL string) string {
	intro, block := credentialSteps(authKind, setupURL)
	more := "\n" + l.column
	return lowerFirst(intro) + ", then add to " + l.style.code(file) + ":" + more + strings.Join(block, more)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func LoginCommand(mini agents.MiniEntry, server string) string {
	return miniCommand(AgentConnections{Mini: mini}, "auth", server)
}

// Commands carry the config directory whenever agents are given one, so they act on the same servers.
func miniCommand(c AgentConnections, args ...string) string {
	mini := c.Mini
	words := []string{shellQuote(c.miniName())}
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

// Widest is the longest text among items, for padding a column.
func Widest[T any](items []T, text func(T) string) int {
	width := 0
	for _, item := range items {
		width = max(width, len(text(item)))
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

func envStep(unset *config.UnsetEnvError, file string) string {
	them := "them"
	if len(unset.Names) == 1 {
		them = "it"
	}
	return fmt.Sprintf("set %s where mini runs (used in %s), or edit %s", them, unset.Field, file)
}
