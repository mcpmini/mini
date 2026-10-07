package tui

import (
	"net/url"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

// LoadedCatalog is the catalog to offer. Unavailable says why it isn't the published one.
type LoadedCatalog struct {
	Catalog     catalog.Catalog
	Unavailable error
}

type catalogLoaded struct {
	result LoadedCatalog
	err    error
}

type catalogScreen struct {
	load func() (LoadedCatalog, error)
	// offered drops the servers mini already has.
	offered func([]catalog.Entry) []catalog.Entry
	// imports is what the Import screen has ticked; a catalog server it covers is hidden.
	imports func() []config.ServerConfig
	loaded  bool
	loadErr error
	// unavailable says why the published catalog wasn't used.
	unavailable error
	popular     []string
	available   []catalog.Entry
	checked     map[string]bool
	list        *list
}

type catalogParams struct {
	load    func() (LoadedCatalog, error)
	offered func([]catalog.Entry) []catalog.Entry
	imports func() []config.ServerConfig
}

func newCatalogScreen(p catalogParams) *catalogScreen {
	s := &catalogScreen{load: p.load, offered: p.offered, imports: p.imports, checked: map[string]bool{}}
	s.enter()
	return s
}

func (s *catalogScreen) start() tea.Cmd {
	if s.loaded {
		return nil
	}
	return func() tea.Msg {
		result, err := s.load()
		return catalogLoaded{result: result, err: err}
	}
}

func (s *catalogScreen) update(msg tea.Msg) {
	loaded, ok := msg.(catalogLoaded)
	if !ok {
		return
	}
	s.loaded, s.loadErr = true, loaded.err
	c := loaded.result.Catalog
	s.unavailable, s.popular = loaded.result.Unavailable, c.Popular
	if loaded.err == nil {
		s.available = initcmd.GroupByCategory(s.offered(c.Entries))
	}
	s.enter()
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
	s.list.header = row{label: "SERVER", detail: "URL"}
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
	return row{key: e.Name, label: e.Name, detail: host(e.URL), section: section, search: e.Title + " " + e.Description}
}

// The host sits next to every fetched name, so a catalog entry can't pass itself off as another service.
func host(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return rawURL
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
	switch {
	case !s.loaded:
		return "loading…"
	case s.loadErr != nil:
		return "The catalog couldn't be loaded: " + s.loadErr.Error()
	case len(s.available) == 0:
		return "mini already has every server in the catalog."
	case s.unavailable != nil:
		note := "Showing the built-in catalog: the published one is unavailable (" + s.unavailable.Error() + ")"
		return dim.Render(note) + "\n\n" + s.list.view(height-2)
	}
	return s.list.view(height)
}

func (s *catalogScreen) keys() string {
	return s.list.keys("space tick · / filter · enter continue")
}

func (s *catalogScreen) filterLine() string {
	return s.list.filterLine()
}

// Until it loads the screen isn't empty: the user waits on it rather than skipping it.
func (s *catalogScreen) empty() bool {
	return s.loaded && s.loadErr == nil && len(s.shown()) == 0
}

func (s *catalogScreen) picks() []catalog.Entry {
	return slices.DeleteFunc(s.shown(), func(e catalog.Entry) bool { return !s.checked[e.Name] })
}
