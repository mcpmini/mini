package initcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
	writeHandSteps(b, c.NoMini, c.Mini)
}

func writeHandSteps(b *strings.Builder, list []agents.Agent, mini agents.MiniEntry) {
	fmt.Fprintln(b, "\nTo connect mini to your agents:")
	for _, agent := range list {
		fmt.Fprintf(b, "  %s (%s):\n%s\n", agent.Name, agent.ConfigPath, indent(handConnectStep(agent, mini), "    "))
	}
}

func writeConnected(b *strings.Builder, r Report) {
	writeInactiveMini(b, r.Agents)
	var changed []string
	for _, result := range r.Connected {
		writeAgentResult(b, r.Agents.Mini, result)
		if result.Err == nil && (result.Backup != "" || result.Created) {
			changed = append(changed, result.Agent.Name)
		}
	}
	if left := notTried(r); len(left) > 0 {
		writeHandSteps(b, left, r.Agents.Mini)
	}
	if len(changed) > 0 {
		fmt.Fprintf(b, "\nRestart %s to start using mini.\n", JoinAnd(changed))
	}
}

// An agent init tried and failed to connect already got its hand step, with the error.
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

func writeAgentResult(b *strings.Builder, mini agents.MiniEntry, result AgentResult) {
	name, file := result.Agent.Name, result.Agent.ConfigPath
	switch {
	case result.Err != nil:
		fmt.Fprintf(b, "\nCouldn't connect %s: %v\nAdd mini to %s by hand:\n%s\n",
			name, result.Err, file, indent(handConnectStep(result.Agent, mini), "  "))
	case result.Created:
		fmt.Fprintf(b, "\n%s: created %s\n", name, file)
	case result.Backup != "":
		fmt.Fprintf(b, "\n%s: %s backed up to %s\n", name, file, result.Backup)
	case result.ExistingMini != NoMiniEntry:
		fmt.Fprintf(b, "\n%s: already has a mini entry; %s is unchanged\n", name, file)
	case len(result.Kept) > 0 || len(result.Changed) > 0:
		fmt.Fprintf(b, "\n%s: %s is unchanged\n", name, file)
	}
	for _, kept := range result.Kept {
		fmt.Fprintf(b, "  %s stays in %s: %s\n", kept.Entry, name, keptReason(kept))
	}
	for _, entry := range result.Changed {
		fmt.Fprintf(b, "  %s stays in %s: it changed after it was checked\n", entry, name)
	}
}

func keptReason(kept KeptEntry) string {
	if errors.Is(kept.Err, errNotChecked) || errors.Is(kept.Err, errMiniInactive) {
		return kept.Err.Error()
	}
	// The check's error is a protocol detail; what's left to do is in the summary's finishing steps.
	return "it doesn't work in mini yet"
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
