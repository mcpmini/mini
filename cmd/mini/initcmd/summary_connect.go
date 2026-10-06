package initcmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
)

// HandConnectSteps is how to connect mini to each agent by hand; an agent already running mini gets no step.
func HandConnectSteps(configDir, selfPath string, list []agents.Agent) string {
	var b strings.Builder
	writeHandConnect(&b, ClassifyAgents(configDir, selfPath, list))
	return b.String()
}

func writeHandConnect(b *strings.Builder, c AgentConnections) {
	writeInactiveMini(b, c)
	if len(c.NoMini) == 0 {
		if len(c.MiniServes) > 0 || len(c.MiniInactive) > 0 {
			return
		}
		fmt.Fprintf(
			b,
			"\nTo connect mini to your agent, add it to its MCP config:\n%s\n",
			indent(jsonSnippet(c.Mini), "  "),
		)
		return
	}
	fmt.Fprintln(b, "\nTo connect mini to your agents:")
	for _, agent := range c.NoMini {
		fmt.Fprintf(b, "  %s (%s):\n%s\n", agent.Name, agent.ConfigPath, indent(handConnectStep(agent, c.Mini), "    "))
	}
}

func writeInactiveMini(b *strings.Builder, c AgentConnections) {
	for _, agent := range c.MiniInactive {
		fmt.Fprintf(b, "\n%s (%s) has a mini entry that may not run these servers: it's switched off, uses another "+
			"config directory, or doesn't name mini by absolute path. To use them, have it run: %s\n",
			agent.Name, agent.ConfigPath, shellCommand(c.Mini))
	}
}

func handConnectStep(agent agents.Agent, mini agents.MiniEntry) string {
	switch agent.Name {
	case "Claude Code":
		return "claude mcp add --scope user " + agents.MiniKey + " -- " + shellCommand(mini)
	case "Codex":
		return codexSnippet(mini)
	}
	return jsonSnippet(mini)
}

func codexSnippet(mini agents.MiniEntry) string {
	args := make([]string, len(mini.Args))
	for i, arg := range mini.Args {
		args[i] = strconv.Quote(arg)
	}
	return fmt.Sprintf(
		"[mcp_servers.%s]\ncommand = %s\nargs = [%s]",
		agents.MiniKey,
		strconv.Quote(mini.Command),
		strings.Join(args, ", "),
	)
}

func jsonSnippet(mini agents.MiniEntry) string {
	doc := map[string]any{
		"mcpServers": map[string]any{agents.MiniKey: map[string]any{"command": mini.Command, "args": mini.Args}},
	}
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
