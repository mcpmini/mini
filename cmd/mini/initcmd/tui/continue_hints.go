package tui

import (
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/ops"
)

const (
	browsePopularHint = "browse popular MCPs"
	logInHint         = "log in to your MCPs"
	addToAgentsHint   = "add mini to your agents"
)

func (ui screens) importHint() string {
	if !ui.catalogs.empty() {
		return browsePopularHint
	}
	return ui.catalogHint()
}

func (ui screens) catalogHint() string {
	switch {
	case ui.loginsLikely():
		return logInHint
	case ui.connectWillShow():
		return addToAgentsHint
	}
	return ""
}

func (ui screens) loginsHint() string {
	if ui.connectWillShow() {
		return addToAgentsHint
	}
	return ""
}

// An agent without mini is listed on Connect even before Connect has been visited, so it counts now.
func (ui screens) connectWillShow() bool {
	return !ui.connects.empty() || ui.agentWithoutMini
}

// Logins is only known to be non-empty once it has been visited: a server already in mini that needs
// a login is not predicted before then.
func (ui screens) loginsLikely() bool {
	return !ui.logins.empty() || ui.pickNeedsLogin() || ui.tickedMayNeedOAuth()
}

func (ui screens) pickNeedsLogin() bool {
	return slices.ContainsFunc(ui.catalogs.picks(), func(e catalog.Entry) bool {
		return e.Auth != catalog.AuthNone
	})
}

func (ui screens) tickedMayNeedOAuth() bool {
	return slices.ContainsFunc(ui.imports.ticked(), ops.MayNeedOAuth)
}

func anyAgentWithout(withMini map[string]bool, list []agents.Agent) bool {
	return slices.ContainsFunc(list, func(a agents.Agent) bool { return !withMini[a.Name] })
}
