package initcmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

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

// Summary is what init prints when it ends: a headline, then only what the user still has to act
// on. After an interactive run it leaves out what init's screens already showed.
func Summary(r Report, style SummaryStyle) string {
	var b strings.Builder
	writeHeadline(&b, r)
	if !r.Interactive {
		writeWhatNoScreenShowed(&b, r)
	}
	writeSkipped(&b, r)
	writeImportNotes(&b, r)
	writeFailures(&b, r)
	if r.Connected == nil {
		writeNotConnected(&b, r.Agents, style)
	} else {
		writeConnected(&b, r, style)
	}
	return b.String()
}

func writeHeadline(b *strings.Builder, r Report) {
	if len(r.Added) == 0 {
		fmt.Fprintln(b, "No MCPs were added to mini.")
		return
	}
	fmt.Fprintf(b, "Added %s to mini: %s.\n", Plural(len(r.Added), "MCP"), JoinAnd(r.Added))
}

// An imported server can start differently in mini than it did in the agent, which no screen showed.
func writeImportNotes(b *strings.Builder, r Report) {
	for _, name := range slices.Sorted(maps.Keys(r.Import.DroppedSettings)) {
		file := config.ServerPath(r.ConfigDir, name)
		fmt.Fprintln(b, IgnoredSettingsNote(name, r.Import.DroppedSettings[name], file))
	}
	for _, name := range slices.Sorted(maps.Keys(r.Import.StaticHeaders)) {
		for _, note := range StaticHeaderNotes(name, r.Import.StaticHeaders[name], config.ServerPath(r.ConfigDir, name)) {
			fmt.Fprintln(b, note)
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
			"Could not read %s's config (%s), so none of its MCPs were imported: %v\n",
			u.Agent,
			u.ConfigPath,
			u.Err,
		)
	}
	for _, failure := range r.WriteErrors {
		fmt.Fprintf(b, "Could not add %s: %v\n", failure.Name, failure.Err)
	}
	if r.ReadServersErr != nil {
		fmt.Fprintf(b, "Could not read mini's servers: %v\n", r.ReadServersErr)
	}
}

// LoginCommands gives the command that logs in to a server, written as the summary writes its commands.
func LoginCommands(configDir string) func(server string) string {
	c := AgentConnections{Mini: MiniCommand(configDir), MiniName: systemLookup.displayName()}
	return func(server string) string { return miniCommand(c, "auth", server) }
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
