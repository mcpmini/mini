package projection

import (
	"sort"

	"github.com/mcpmini/mini/internal/jq"
)

func collapseExcluded(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		wk := jq.CollapseIndex(p)
		if !seen[wk] {
			seen[wk] = true
			out = append(out, wk)
		}
	}
	sort.Strings(out)
	return out
}
