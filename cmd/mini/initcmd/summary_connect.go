package initcmd

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
)

func writeNotConnected(b *strings.Builder, c AgentConnections, style SummaryStyle) {
	writeInactiveMini(b, c)
	switch {
	case len(c.NoMini) > 0:
		writeConnectLater(b, c, c.NoMini, style)
	case len(c.MiniServes) == 0 && len(c.MiniInactive) == 0:
		writeNoAgents(b, c, style)
	}
}

func writeNoAgents(b *strings.Builder, c AgentConnections, style SummaryStyle) {
	fmt.Fprintln(b, style.warning("⚠ No agents were found automatically"))
	// Agents started from the desktop get a minimal PATH, so the entry needs mini's full path.
	command := shellCommand(c.Mini)
	fmt.Fprintf(b, "To use mini, add it to your agent as an MCP server with the command: %s\n", style.code(command))
}

func writeConnectLater(b *strings.Builder, c AgentConnections, list []agents.Agent, style SummaryStyle) {
	fmt.Fprintf(
		b,
		"To connect mini to %s later, run: %s\n",
		JoinAnd(agentNames(list)),
		style.code(miniCommand(c, "init")),
	)
}

func writeConnected(b *strings.Builder, r Report, style SummaryStyle) {
	writeInactiveMini(b, r.Agents)
	var changed []string
	for _, result := range r.Connected {
		switch {
		case result.Err != nil:
			fmt.Fprintf(b, "Could not connect mini to %s: %v\n", result.Agent.Name, result.Err)
		case result.Backup != "":
			fmt.Fprintf(b, "%s's config was backed up to %s\n", result.Agent.Name, style.code(result.Backup))
		}
		if result.Err == nil && (result.Backup != "" || result.Created) {
			changed = append(changed, result.Agent.Name)
		}
	}
	if len(changed) > 0 {
		them := "them"
		if len(changed) == 1 {
			them = "it"
		}
		fmt.Fprintf(b, "Connected mini to %s. Restart %s to start using mini.\n", JoinAnd(changed), them)
	}
	if left := notTried(r); len(left) > 0 {
		writeConnectLater(b, r.Agents, left, style)
	}
}

func agentNames(list []agents.Agent) []string {
	var names []string
	for _, agent := range list {
		names = append(names, agent.Name)
	}
	return names
}

// An agent init tried and failed to connect already has its error line.
func notTried(r Report) []agents.Agent {
	var left []agents.Agent
	for _, agent := range r.Agents.NoMini {
		tried := slices.ContainsFunc(
			r.Connected,
			func(result AgentResult) bool { return result.Agent.Name == agent.Name },
		)
		if !tried {
			left = append(left, agent)
		}
	}
	return left
}

// JoinAnd lists names as people write them: "a", "a and b", "a, b and c".
func JoinAnd(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func writeInactiveMini(b *strings.Builder, c AgentConnections) {
	for _, agent := range c.MiniInactive {
		fmt.Fprintf(b, "\n%s (%s) has a mini entry that may not run these servers: %s. To use them, have it run: %s\n",
			agent.Name, agent.ConfigPath, inactiveMiniReasons, shellCommand(c.Mini))
	}
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
