package initcmd

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
)

type rowMerger struct {
	rows []Candidate
	// suffixed marks rows that took a suffix because another config has their name.
	suffixed []bool
	claimed  map[string]bool
	used     map[string]bool
}

// Entries arrive in agent order, then by name, which fixes which config keeps the plain name; an
// enabled config claims it before a switched-off one, so the server the user runs starts ticked.
func mergeEntries(entries []agentEntry, configured map[string]bool) []Candidate {
	m := rowMerger{claimed: map[string]bool{}, used: maps.Clone(configured)}
	for _, e := range entries {
		m.used[NormalizeName(e.name)] = true
	}
	ordered := slices.Clone(entries)
	slices.SortStableFunc(ordered, func(a, b agentEntry) int { return boolOrder(a.server.Disabled, b.server.Disabled) })
	for _, e := range ordered {
		m.add(e)
	}
	keepAgentOrder(m.rows, entries)
	slices.SortFunc(m.rows, func(a, b Candidate) int { return strings.Compare(a.Name, b.Name) })
	return m.rows
}

func keepAgentOrder(rows []Candidate, entries []agentEntry) {
	position := map[[2]string]int{}
	for i, e := range entries {
		position[[2]string{e.agent, e.name}] = i
	}
	for _, row := range rows {
		slices.SortStableFunc(row.Sources, func(a, b Source) int {
			return position[[2]string{a.Agent, a.Name}] - position[[2]string{b.Agent, b.Name}]
		})
	}
}

func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case b:
		return -1
	}
	return 1
}

// One server under several names is still one row: importing it twice would expose its tools twice.
func (m *rowMerger) add(e agentEntry) {
	source := Source{
		Agent: e.agent, Name: e.name, Entry: e.server.Config, Disabled: e.server.Disabled,
		IgnoredRunSettings: e.server.IgnoredRunSettings, UnusedEnvHeaders: e.server.UnusedEnvHeaders,
	}
	i := slices.IndexFunc(m.rows, func(row Candidate) bool { return agents.SameServer(row.Config, e.server.Config) })
	if i >= 0 {
		m.rows[i].Sources = append(m.rows[i].Sources, source)
		m.rows[i].Checked = m.rows[i].Checked || (!m.suffixed[i] && !e.server.Disabled)
		return
	}
	row := Candidate{Name: NormalizeName(e.name), Sources: []Source{source}, Checked: !e.server.Disabled}
	suffixed := m.claimed[row.Name]
	if suffixed {
		// A second config under the same name would expose the same tools twice if ticked by default.
		row.Name, row.Checked = m.nextFreeName(row.Name), false
	}
	m.claimed[row.Name] = true
	row.Config = e.server.Config
	row.Config.Name = row.Name
	m.rows = append(m.rows, row)
	m.suffixed = append(m.suffixed, suffixed)
}

func (m *rowMerger) nextFreeName(name string) string {
	for n := 2; ; n++ {
		candidate := name + "-" + strconv.Itoa(n)
		if !m.used[candidate] {
			m.used[candidate] = true
			return candidate
		}
	}
}
