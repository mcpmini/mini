package tui

const (
	browsePopularHint = "browse popular MCPs"
	addToAgentsHint   = "add mini to your agents"
)

func importHint(catalogs *catalogScreen) func() string {
	return func() string {
		if catalogs.empty() {
			return ""
		}
		return browsePopularHint
	}
}

// Connect's list is read when Logins opens, so the hint knows whether Connect follows.
func loginsHint(connects *connectScreen) func() string {
	return func() string {
		if connects.empty() {
			return ""
		}
		return addToAgentsHint
	}
}
