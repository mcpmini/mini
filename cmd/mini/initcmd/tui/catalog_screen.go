package tui

import (
	"net/url"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

type catalogScreen struct {
	popular   []string
	available []catalog.Entry
	// imports is what the Import screen has ticked; a catalog server it covers is hidden.
	imports func() []config.ServerConfig
	checked map[string]bool
	list    *list
}

func newCatalogScreen(
	c catalog.Catalog,
	available []catalog.Entry,
	imports func() []config.ServerConfig,
) *catalogScreen {
	s := &catalogScreen{
		popular:   c.Popular,
		available: initcmd.GroupByCategory(available),
		imports:   imports,
		checked:   map[string]bool{},
	}
	s.enter()
	return s
}

// enter rebuilds the rows, since going back to Import may have ticked a server the catalog has.
// The Import row wins: the catalog's tick on that server is dropped.
func (s *catalogScreen) enter() {
	shown := s.shown()
	for name := range s.checked {
		if !slices.ContainsFunc(shown, func(e catalog.Entry) bool { return e.Name == name }) {
			delete(s.checked, name)
		}
	}
	s.list = newList(s.rows(shown), s.checked)
}

func (s *catalogScreen) shown() []catalog.Entry {
	covered := initcmd.NewConfiguredKeys(s.imports())
	return slices.DeleteFunc(slices.Clone(s.available), covered.Has)
}

func (s *catalogScreen) rows(shown []catalog.Entry) []row {
	var rows []row
	for _, name := range s.popular {
		if i := slices.IndexFunc(shown, func(e catalog.Entry) bool { return e.Name == name }); i >= 0 {
			r := entryRow(shown[i], "Popular")
			r.repeated = true
			rows = append(rows, r)
		}
	}
	for _, e := range shown {
		rows = append(rows, entryRow(e, e.Category))
	}
	return rows
}

func entryRow(e catalog.Entry, section string) row {
	detail := host(e.URL)
	if setup := setupLabel(e.Auth); setup != "" {
		detail += "  " + setup
	}
	return row{key: e.Name, label: e.Name, detail: detail, section: section, search: e.Title + " " + e.Description}
}

// The host sits next to every fetched name, so a catalog entry can't pass itself off as another service.
func host(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return rawURL
}

func setupLabel(auth string) string {
	switch auth {
	case catalog.AuthToken:
		return "needs a token"
	case catalog.AuthOAuth2App:
		return "needs your own OAuth app"
	}
	return ""
}

func (s *catalogScreen) heading() string {
	return "Add servers from the catalog"
}

func (s *catalogScreen) handle(key tea.KeyPressMsg) step {
	if s.list.handle(key) {
		return stay
	}
	switch key.String() {
	case "enter":
		return forward
	case "esc", "left", "shift+tab":
		return back
	}
	return stay
}

func (s *catalogScreen) body(height int) string {
	return s.list.view(height)
}

func (s *catalogScreen) keys() string {
	return s.list.keys("space tick · / filter · enter continue")
}

func (s *catalogScreen) filterLine() string {
	return s.list.filterLine()
}

func (s *catalogScreen) empty() bool {
	return len(s.available) == 0
}

func (s *catalogScreen) picks() []catalog.Entry {
	return slices.DeleteFunc(s.shown(), func(e catalog.Entry) bool { return !s.checked[e.Name] })
}
