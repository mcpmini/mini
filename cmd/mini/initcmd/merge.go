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

// Entries arrive in agent order, then by name, which fixes which config keeps the plain name.
func mergeEntries(entries []agentEntry, configured map[string]bool) []Candidate {
	m := rowMerger{claimed: map[string]bool{}, used: maps.Clone(configured)}
	for _, e := range entries {
		m.used[NormalizeName(e.name)] = true
	}
	for _, e := range entries {
		m.add(e)
	}
	slices.SortFunc(m.rows, func(a, b Candidate) int { return strings.Compare(a.Name, b.Name) })
	return m.rows
}

// One server under several names is still one row: importing it twice would expose its tools twice.
func (m *rowMerger) add(e agentEntry) {
	source := Source{Agent: e.agent, Name: e.name, Entry: e.server.Config, IgnoredRunSettings: e.server.IgnoredRunSettings}
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
