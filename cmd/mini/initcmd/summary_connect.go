package initcmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
)

func writeConnected(b *strings.Builder, r Report) {
	var changed []string
	for _, result := range r.Connected {
		writeAgentResult(b, r, result)
		if result.Err == nil && (result.Backup != "" || result.Created) {
			changed = append(changed, result.Agent.Name)
		}
	}
	if len(changed) > 0 {
		fmt.Fprintf(b, "\nRestart %s to start using mini.\n", joinAnd(changed))
	}
}

func writeAgentResult(b *strings.Builder, r Report, result AgentResult) {
	name, file := result.Agent.Name, result.Agent.ConfigPath
	switch {
	case result.Err != nil:
		fmt.Fprintf(b, "\nCould not connect %s: %v\nAdd mini to %s by hand:\n%s\n", name, result.Err, file, indent(manualStep(result.Agent, r.Mini), "  "))
		return
	case result.Created:
		fmt.Fprintf(b, "\n%s: created %s; to undo: rm %s\n", name, file, shellQuote(file))
	case result.Backup != "":
		fmt.Fprintf(b, "\n%s: %s backed up to %s; to undo: cp %s %s\n", name, file, result.Backup, shellQuote(result.Backup), shellQuote(file))
	}
	for _, kept := range result.Kept {
		fmt.Fprintf(b, "  %s stays in %s: mini's %s failed its connection check: %v\n", kept.Entry, name, kept.Server, kept.Err)
	}
	for _, entry := range result.Changed {
		fmt.Fprintf(b, "  %s stays in %s: it changed after it was checked\n", entry, name)
	}
}

func writeManualConnect(b *strings.Builder, r Report) {
	writeInactiveMini(b, r)
	if len(r.Unconnected) == 0 {
		if len(r.HasMini) > 0 || len(r.InactiveMini) > 0 {
			return
		}
		fmt.Fprintf(b, "\nTo connect mini to your agent, add it to its MCP config:\n%s\n", indent(jsonSnippet(r.Mini), "  "))
		return
	}
	fmt.Fprintln(b, "\nTo connect mini to your agents:")
	for _, agent := range r.Unconnected {
		fmt.Fprintf(b, "  %s (%s):\n%s\n", agent.Name, agent.ConfigPath, indent(manualStep(agent, r.Mini), "    "))
	}
}

func writeInactiveMini(b *strings.Builder, r Report) {
	for _, agent := range r.InactiveMini {
		fmt.Fprintf(b, "\n%s (%s) has a mini entry that may not run these servers: it's switched off, uses another "+
			"config directory, or doesn't name mini by absolute path. To use them, have it run: %s\n",
			agent.Name, agent.ConfigPath, shellCommand(r.Mini))
	}
}

func manualStep(agent agents.Agent, mini agents.MiniEntry) string {
	switch {
	case agent.Name == "Claude Code":
		return "claude mcp add --scope user " + agents.MiniKey + " -- " + shellCommand(mini)
	case agent.RemoveDisables:
		return codexSnippet(mini)
	}
	return jsonSnippet(mini)
}

func codexSnippet(mini agents.MiniEntry) string {
	args := make([]string, len(mini.Args))
	for i, arg := range mini.Args {
		args[i] = strconv.Quote(arg)
	}
	return fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = [%s]", agents.MiniKey, strconv.Quote(mini.Command), strings.Join(args, ", "))
}

func jsonSnippet(mini agents.MiniEntry) string {
	doc := map[string]any{"mcpServers": map[string]any{agents.MiniKey: map[string]any{"command": mini.Command, "args": mini.Args}}}
	data, _ := json.MarshalIndent(doc, "", "  ") //nolint:errcheck // strings and string slices always marshal
	return string(data)
}

func shellCommand(mini agents.MiniEntry) string {
	words := []string{shellQuote(mini.Command)}
	for _, arg := range mini.Args {
		words = append(words, shellQuote(arg))
	}
	return strings.Join(words, " ")
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

func joinAnd(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
