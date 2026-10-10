package tui

import (
	"cmp"
	"net/url"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

type catalogLoaded struct {
	catalog catalog.Catalog
	err     error
}

type catalogScreen struct {
	load func() (catalog.Catalog, error)
	// offered drops the servers mini already has.
	offered func([]catalog.Entry) []catalog.Entry
	// imports is what the Import screen has ticked; a catalog server it covers is hidden.
	imports       func() []config.ServerConfig
	loaded        bool
	loadErr       error
	popular       []string
	loadedEntries []catalog.Entry
	available     []catalog.Entry
	checked       map[string]bool
	grid          *catalogGrid
	width, height int
	back          bool
}

type catalogParams struct {
	load    func() (catalog.Catalog, error)
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
		c, err := s.load()
		return catalogLoaded{catalog: c, err: err}
	}
}

func (s *catalogScreen) update(msg tea.Msg) tea.Cmd {
	loaded, ok := msg.(catalogLoaded)
	if !ok {
		return nil
	}
	s.loaded, s.loadErr = true, loaded.err
	c := loaded.catalog
	s.popular, s.loadedEntries = c.Popular, c.Entries
	if loaded.err == nil {
		s.available = initcmd.GroupByCategory(s.offered(c.Entries))
	}
	s.enter()
	return nil
}

// enter rebuilds the rows, since going back to Import may have ticked a server the catalog has.
// The Import row wins: the catalog's tick on that server is dropped.
func (s *catalogScreen) enter() tea.Cmd {
	shown := s.shown()
	for name := range s.checked {
		if !slices.ContainsFunc(shown, func(e catalog.Entry) bool { return e.Name == name }) {
			delete(s.checked, name)
		}
	}
	s.grid = newCatalogGrid(s.sections(shown), s.checked)
	s.grid.resize(s.width, s.height)
	s.grid.actions.offerBack(s.back)
	if s.loaded && !s.offersEntries() {
		s.grid.actions.reach()
	}
	return nil
}

func (s *catalogScreen) offerBack(back bool) {
	s.back = back
	s.grid.actions.offerBack(back)
}

func (s *catalogScreen) resize(width, height int) {
	s.width, s.height = width, height
	s.grid.resize(width, height)
}

func (s *catalogScreen) shown() []catalog.Entry {
	covered := initcmd.NewConfiguredKeys(s.imports())
	return slices.DeleteFunc(slices.Clone(s.available), covered.Has)
}

// Popular keeps the catalog's ranking; every other section is alphabetical, so a name is easy to find.
func (s *catalogScreen) sections(shown []catalog.Entry) []gridSection {
	popular := gridSection{title: "Popular", repeated: true}
	for _, name := range s.popular {
		if i := slices.IndexFunc(shown, func(e catalog.Entry) bool { return e.Name == name }); i >= 0 {
			popular.entries = append(popular.entries, catalogGridEntry(shown[i]))
		}
	}
	var sections []gridSection
	if len(popular.entries) > 0 {
		sections = append(sections, popular)
	}
	for _, e := range shown {
		if len(sections) == 0 || sections[len(sections)-1].title != e.Category {
			sections = append(sections, gridSection{title: e.Category})
		}
		last := &sections[len(sections)-1]
		last.entries = append(last.entries, catalogGridEntry(e))
	}
	for i := range sections {
		if !sections[i].repeated {
			slices.SortStableFunc(sections[i].entries, func(a, b gridEntry) int {
				return strings.Compare(strings.ToLower(a.title), strings.ToLower(b.title))
			})
		}
	}
	return sections
}

func catalogGridEntry(e catalog.Entry) gridEntry {
	return gridEntry{key: e.Name, title: cmp.Or(e.Title, e.Name), host: host(e.URL), description: e.Description}
}

func host(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return rawURL
}

const (
	needsToken  = "needs a token"
	needsOwnApp = "needs your own OAuth app"
)

func (s *catalogScreen) heading() string {
	return "Add servers from the catalog"
}

func (s *catalogScreen) handle(key tea.KeyPressMsg) (step, tea.Cmd) {
	// Until the catalog arrives there is nothing to pick, and enter would skip it unseen.
	if !s.offersEntries() {
		return s.handleWithoutGrid(key)
	}
	if s.grid.handle(key) {
		return stay, nil
	}
	switch key.String() {
	case "enter":
		return s.grid.actions.step(), nil
	case "esc":
		return back, nil
	}
	return stay, nil
}

// Until the catalog loads there is nothing to act on; once it fails or offers nothing new, only
// the actions under the message are left.
func (s *catalogScreen) handleWithoutGrid(key tea.KeyPressMsg) (step, tea.Cmd) {
	switch key.String() {
	case "up", "down":
		if s.loaded {
			s.grid.actions.moveWithin(direction(key.String()))
		}
	case "enter":
		if s.loaded {
			return s.grid.actions.step(), nil
		}
	case "esc":
		return back, nil
	}
	return stay, nil
}

func (s *catalogScreen) body(height int) string {
	switch {
	case !s.loaded:
		return "loading…"
	case s.loadErr != nil:
		return s.withActions("The catalog couldn't be loaded: " + s.loadErr.Error())
	case len(s.available) == 0:
		return s.withActions("mini already has every server in the catalog.")
	}
	return s.grid.view(height)
}

func (s *catalogScreen) withActions(message string) string {
	return s.grid.actions.withMessage(message)
}

func (s *catalogScreen) offersEntries() bool {
	return s.loaded && s.loadErr == nil && len(s.available) > 0
}

func (s *catalogScreen) keys() string {
	if !s.loaded {
		return "loading the catalog"
	}
	switch {
	case !s.offersEntries():
		return s.grid.actions.keys(false)
	case s.grid.actions.active:
		return s.grid.keys(s.grid.actions.keys(true))
	case s.grid.isGrid():
		return s.grid.keys("↑↓←→ move · space/enter tick · tab continue · / filter")
	}
	return s.grid.keys("↑↓ move · space/enter tick or open · tab continue · / filter")
}

func (s *catalogScreen) filterLine() string {
	return s.grid.filter.line()
}

// entries is the catalog the screen offered from, once it has loaded.
func (s *catalogScreen) entries() ([]catalog.Entry, bool) {
	return s.loadedEntries, s.loaded && s.loadErr == nil
}

// Until it loads the screen isn't empty: the user waits on it rather than skipping it.
func (s *catalogScreen) empty() bool {
	return s.loaded && s.loadErr == nil && len(s.shown()) == 0
}

func (s *catalogScreen) picks() []catalog.Entry {
	return slices.DeleteFunc(s.shown(), func(e catalog.Entry) bool { return !s.checked[e.Name] })
}
