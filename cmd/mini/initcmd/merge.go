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
	// byName holds the rows for each normalized name, including the suffixed ones.
	byName map[string][]int
	used   map[string]bool
}

// Entries arrive in agent order, then by name, which fixes which config keeps the plain name.
func mergeEntries(entries []agentEntry, configured map[string]bool) []Candidate {
	m := rowMerger{byName: map[string][]int{}, used: maps.Clone(configured)}
	for _, e := range entries {
		m.used[NormalizeName(e.name)] = true
	}
	for _, e := range entries {
		m.add(e)
	}
	slices.SortFunc(m.rows, func(a, b Candidate) int { return strings.Compare(a.Name, b.Name) })
	return m.rows
}

func (m *rowMerger) add(e agentEntry) {
	name := NormalizeName(e.name)
	source := Source{Agent: e.agent, Name: e.name, Entry: e.server.Config}
	for n, i := range m.byName[name] {
		if agents.SameServer(m.rows[i].Config, e.server.Config) {
			m.rows[i].Sources = append(m.rows[i].Sources, source)
			m.rows[i].Checked = m.rows[i].Checked || (n == 0 && !e.server.Disabled)
			return
		}
	}
	row := Candidate{Name: name, Sources: []Source{source}, Checked: !e.server.Disabled}
	if len(m.byName[name]) > 0 {
		// A second config under the same name would expose the same tools twice if ticked by default.
		row.Name, row.Checked = m.nextFreeName(name), false
	}
	row.Config = e.server.Config
	row.Config.Name = row.Name
	m.byName[name] = append(m.byName[name], len(m.rows))
	m.rows = append(m.rows, row)
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
