package initcmd

import (
	"context"
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
	if len(c.NoMini) == 0 && (len(c.MiniServes) > 0 || len(c.MiniInactive) > 0) {
		return
	}
	writeConnectSteps(b, c, c.NoMini)
}

func writeConnectSteps(b *strings.Builder, c AgentConnections, list []agents.Agent) {
	switch {
	case c.TemporaryMini != "":
		fmt.Fprintf(b, "\nTo connect mini to %s, %s.\n", agentsPhrase(list), c.installFirst())
	case len(list) == 0:
		fmt.Fprintf(
			b,
			"\nTo connect mini to your agent, add it to its MCP config:\n%s\n",
			indent(jsonSnippet(c.Mini), "  "),
		)
	default:
		writeHandSteps(b, list, c.Mini)
	}
}

// InstallPermanently is the step for a mini that gets cleaned up. An agent's mini entry is never
// rewritten, so a step naming that binary couldn't be undone by init.
const InstallPermanently = "install mini somewhere permanent and run mini init from there"

func (c AgentConnections) installFirst() string {
	return InstallPermanently + "; " + c.TemporaryMini + " gets cleaned up"
}

func agentsPhrase(list []agents.Agent) string {
	if len(list) == 0 {
		return "your agents"
	}
	return JoinAnd(AgentNames(list))
}

func AgentNames(list []agents.Agent) []string {
	var names []string
	for _, agent := range list {
		names = append(names, agent.Name)
	}
	return names
}

func writeHandSteps(b *strings.Builder, list []agents.Agent, mini agents.MiniEntry) {
	fmt.Fprintln(b, "\nTo connect mini to your agents:")
	for _, agent := range list {
		fmt.Fprintf(b, "  %s (%s):\n%s\n", agent.Name, agent.ConfigPath, indent(handConnectStep(agent, mini), "    "))
	}
}

func writeConnected(b *strings.Builder, r Report) {
	writeInactiveMini(b, r.Agents)
	var changed, gotMini []string
	for _, result := range r.Connected {
		writeAgentResult(b, r, result)
		if result.Err == nil && (result.Backup != "" || result.Created) {
			changed = append(changed, result.Agent.Name)
			if result.ExistingMini == NoMiniEntry {
				gotMini = append(gotMini, result.Agent.Name)
			}
		}
	}
	if left := notTried(r); len(left) > 0 {
		writeConnectSteps(b, r.Agents, left)
	}
	if len(gotMini) > 0 && r.Agents.TemporaryMini != "" {
		fmt.Fprintf(b, "\nThe mini entry in %s runs %s, which gets cleaned up: install mini somewhere permanent and "+
			"point the entry at it.\n", JoinAnd(gotMini), r.Agents.TemporaryMini)
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

func writeAgentHeading(b *strings.Builder, r Report, result AgentResult) {
	name, file, mini := result.Agent.Name, result.Agent.ConfigPath, r.Agents.Mini
	switch {
	case result.Err != nil && r.Agents.TemporaryMini != "":
		fmt.Fprintf(b, "\nCouldn't connect %s: %v\nTo connect it, %s.\n", name, result.Err, r.Agents.installFirst())
	case result.Err != nil:
		fmt.Fprintf(b, "\nCouldn't connect %s: %v\nAdd mini to %s by hand:\n%s\n",
			name, result.Err, file, indent(handConnectStep(result.Agent, mini), "  "))
	case result.Created:
		fmt.Fprintf(b, "\n%s: created %s\n", name, file)
	case result.Backup != "":
		fmt.Fprintf(b, "\n%s: %s backed up to %s\n", name, file, result.Backup)
	case result.ExistingMini != NoMiniEntry:
		fmt.Fprintf(b, "\n%s: already has a mini entry; %s is unchanged\n", name, file)
	}
}

func writeAgentResult(b *strings.Builder, r Report, result AgentResult) {
	writeAgentHeading(b, r, result)
	name := result.Agent.Name
	if len(result.Removed) > 0 {
		fmt.Fprintf(b, "  %s %s, which mini runs now\n", removedVerb(result.Agent), JoinAnd(result.Removed))
	}
	for _, kept := range result.Kept {
		fmt.Fprintf(b, "  %s stays in %s: %s\n", kept.Entry, name, keptReason(r, kept))
	}
	for _, entry := range result.Changed {
		fmt.Fprintf(b, "  %s stays in %s: it changed after it was checked\n", entry, name)
	}
}

func removedVerb(agent agents.Agent) string {
	if agent.RemoveDisables {
		return "switched off"
	}
	return "removed"
}

// The check's error is a protocol detail, so the reason says what the user can do next.
func keptReason(r Report, kept KeptEntry) string {
	switch {
	case errors.Is(kept.Err, errNotChecked) || errors.Is(kept.Err, errMiniInactive):
		return kept.Err.Error()
	case r.needsFinishing(kept.Server):
		return "it works in mini once you finish " + kept.Server + " above"
	case errors.Is(kept.Err, context.DeadlineExceeded):
		return "mini's check timed out; " + miniCommand(r.Agents.Mini, "test") + " tries again"
	}
	return "mini couldn't connect to it; " + miniCommand(r.Agents.Mini, "test") + " shows why"
}

func (r Report) needsFinishing(server string) bool {
	return slices.ContainsFunc(r.Servers, func(s ServerStatus) bool { return s.Name == server && s.Readiness != Ready })
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
			"config directory, or doesn't name mini by absolute path. To use them, %s\n",
			agent.Name, agent.ConfigPath, c.inactiveStep())
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

func (c AgentConnections) inactiveStep() string {
	if c.TemporaryMini != "" {
		return "install mini somewhere permanent and have it run that copy; " + c.TemporaryMini + " gets cleaned up."
	}
	return "have it run: " + shellCommand(c.Mini)
}
