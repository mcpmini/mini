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
	load              func() (catalog.Catalog, error)
	inMini            func(catalog.Entry) bool
	importTicked      func() []config.ServerConfig
	loaded            bool
	loadErr           error
	popular           []string
	loadedEntries     []catalog.Entry
	byCategory        []catalog.Entry
	checked           map[string]bool
	grid              *catalogGrid
	width, rowsHeight int
}

type catalogParams struct {
	load         func() (catalog.Catalog, error)
	inMini       func(catalog.Entry) bool
	importTicked func() []config.ServerConfig
}

func newCatalogScreen(p catalogParams) *catalogScreen {
	s := &catalogScreen{load: p.load, inMini: p.inMini, importTicked: p.importTicked, checked: map[string]bool{}}
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
		s.byCategory = initcmd.GroupByCategory(c.Entries)
	}
	s.enter()
	return nil
}

// enter rebuilds the rows, since going back to Import may have ticked a server the catalog has.
// The Import row wins: the catalog's tick on that server is dropped.
func (s *catalogScreen) enter() tea.Cmd {
	offered := s.offered()
	for name := range s.checked {
		if !slices.ContainsFunc(offered, func(e catalog.Entry) bool { return e.Name == name }) {
			delete(s.checked, name)
		}
	}
	s.grid = newCatalogGrid(s.sections(), s.checked)
	s.grid.resize(s.width, s.rowsHeight)
	return nil
}

func (s *catalogScreen) resize(width, rowsHeight int) {
	s.width, s.rowsHeight = width, rowsHeight
	s.grid.resize(width, rowsHeight)
}

func (s *catalogScreen) state(e catalog.Entry, imported initcmd.ConfiguredKeys) entryState {
	switch {
	case s.inMini(e):
		return entryInMini
	case imported.Has(e):
		return entryWillImport
	}
	return entryOffered
}

func (s *catalogScreen) offered() []catalog.Entry {
	imported := initcmd.NewConfiguredKeys(s.importTicked())
	return slices.DeleteFunc(slices.Clone(s.byCategory), func(e catalog.Entry) bool {
		return s.state(e, imported) != entryOffered
	})
}

// Popular keeps the catalog's ranking; every other section is alphabetical, so a name is easy to find.
func (s *catalogScreen) sections() []gridSection {
	imported := initcmd.NewConfiguredKeys(s.importTicked())
	entry := func(e catalog.Entry) gridEntry { return catalogGridEntry(e, s.state(e, imported)) }
	var sections []gridSection
	if popular := s.popularSection(entry); len(popular.entries) > 0 {
		sections = append(sections, popular)
	}
	for _, e := range s.byCategory {
		if len(sections) == 0 || sections[len(sections)-1].title != e.Category {
			sections = append(sections, gridSection{title: e.Category})
		}
		last := &sections[len(sections)-1]
		last.entries = append(last.entries, entry(e))
	}
	sortCategoriesByTitle(sections)
	return sections
}

func (s *catalogScreen) popularSection(entry func(catalog.Entry) gridEntry) gridSection {
	popular := gridSection{title: "Popular", repeated: true}
	for _, name := range s.popular {
		if i := slices.IndexFunc(s.byCategory, func(e catalog.Entry) bool { return e.Name == name }); i >= 0 {
			popular.entries = append(popular.entries, entry(s.byCategory[i]))
		}
	}
	return popular
}

func sortCategoriesByTitle(sections []gridSection) {
	for i := range sections {
		if !sections[i].repeated {
			slices.SortStableFunc(sections[i].entries, func(a, b gridEntry) int {
				return strings.Compare(strings.ToLower(a.title), strings.ToLower(b.title))
			})
		}
	}
}

func catalogGridEntry(e catalog.Entry, state entryState) gridEntry {
	return gridEntry{
		key: e.Name, title: cmp.Or(e.Title, e.Name), host: host(e.URL), description: e.Description, state: state,
	}
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

func (s *catalogScreen) handle(key tea.KeyPressMsg) (reply, tea.Cmd) {
	return s.grid.handle(key), nil
}

// Until the catalog arrives there is nothing to pick, and Continue would skip it unseen.
func (s *catalogScreen) waiting() bool {
	return !s.loaded
}

func (s *catalogScreen) focusable() bool {
	return s.offersEntries() && s.grid.focusable()
}

func (s *catalogScreen) body(height int, focused bool) string {
	switch {
	case !s.loaded:
		return "loading…"
	case s.loadErr != nil:
		return "The catalog couldn't be loaded: " + s.loadErr.Error()
	case len(s.byCategory) == 0:
		return "The catalog lists no servers."
	}
	return s.grid.view(height, focused)
}

func (s *catalogScreen) offersEntries() bool {
	return s.loaded && s.loadErr == nil && len(s.byCategory) > 0
}

func (s *catalogScreen) keys() string {
	switch {
	case !s.loaded:
		return "loading the catalog"
	case s.grid.isGrid():
		return s.grid.keys("↑↓←→ move · space/enter tick · a all · tab continue · / filter")
	}
	return s.grid.keys("↑↓ move · space/enter tick or open · a all · tab continue · / filter")
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
	return s.loaded && s.loadErr == nil && len(s.offered()) == 0
}

func (s *catalogScreen) picks() []catalog.Entry {
	return slices.DeleteFunc(s.offered(), func(e catalog.Entry) bool { return !s.checked[e.Name] })
}
