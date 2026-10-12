package initcmd

import (
	"fmt"
	"strings"
)

// A run without screens never showed which servers were already in mini, so the summary does.
func writeWhatNoScreenShowed(b *strings.Builder, r Report) {
	if len(r.AlreadyConfigured) > 0 {
		fmt.Fprintf(b, "Already configured in mini: %s\n", strings.Join(r.AlreadyConfigured, ", "))
	}
	if len(r.AddCoveredByImport) > 0 {
		fmt.Fprintf(
			b,
			"Imported from your agents instead of the catalog: %s\n",
			strings.Join(r.AddCoveredByImport, ", "),
		)
	}
}
